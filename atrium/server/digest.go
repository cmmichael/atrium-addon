package main

// The week: the last seven whole days of the house in a few lines, from what Home Assistant already recorded.
// What the house used and made, what the electricity came to under the layout's tariff, the water, the nights a
// room's CO2 ran past the "poor" band, the nights a lock stayed unlocked or a door stood open, how often the
// doorbell rang. Nothing
// here is configured: each line appears when the layout names the entity and the recorder has its figures.
//
// Totals come from the recorder's long-term statistics (kept for good, and cut into days by Home Assistant in
// the house's own time zone); locks and rings from its history, which it keeps for ten days unless told
// otherwise. A night is midnight to six.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
	_ "time/tzdata" // Home Assistant names the house's zone; the image carries no zone database

	"github.com/cmmichael/atrium/addon/atrium/server/house"
)

type digestLine struct {
	Kind string `json:"kind"` // energy, solar, cost, water, air, lock, entry, ring
	Text string `json:"text"`
	Warn bool   `json:"warn,omitempty"` // worth a look: a night of poor air, a lock left open
}

// digestDay is one local day's figures, for a chart. A figure the recorder does not have is absent.
type digestDay struct {
	Day   string   `json:"day"`             // 2026-11-02
	Used  *float64 `json:"used,omitempty"`  // kWh
	Solar *float64 `json:"solar,omitempty"` // kWh
	Net   *float64 `json:"net,omitempty"`   // kWh taken from the grid, less what went back
	Cost  *float64 `json:"cost,omitempty"`  // the energy charge with the tax
	Water *float64 `json:"water,omitempty"`
}

type digestEnergy struct {
	Used        *float64 `json:"used,omitempty"`
	UsedBefore  *float64 `json:"usedBefore,omitempty"` // the seven days before these
	Solar       *float64 `json:"solar,omitempty"`
	SolarBefore *float64 `json:"solarBefore,omitempty"`
	Net         *float64 `json:"net,omitempty"`
	Cost        *float64 `json:"cost,omitempty"` // with the fixed charge's share of the week and the tax
	Currency    string   `json:"currency,omitempty"`
}

type digestWater struct {
	Total  float64  `json:"total"`
	Before *float64 `json:"before,omitempty"`
	Unit   string   `json:"unit,omitempty"`
}

type digestAir struct {
	Room   string  `json:"room"`
	Name   string  `json:"name"`
	Nights int     `json:"nights"` // nights with an hour whose average was past the band
	Peak   float64 `json:"peak,omitempty"`
	Limit  float64 `json:"limit"`
}

type digestLock struct {
	Entity string `json:"entity"`
	Name   string `json:"name"`
	Nights int    `json:"nights"` // nights it stood unlocked for an hour or more
}

// digestEntry is a door, a gate or a garage door the layout watches.
type digestEntry struct {
	Entity string `json:"entity"`
	Name   string `json:"name"`
	Nights int    `json:"nights"` // nights it stood open for an hour or more
}

type digestRing struct {
	Entity string `json:"entity"` // the doorbell's camera
	Name   string `json:"name"`
	Count  int    `json:"count"`
}

type digestReport struct {
	From    time.Time     `json:"from"`
	To      time.Time     `json:"to"`
	Zone    string        `json:"zone,omitempty"`
	Lines   []digestLine  `json:"lines"`
	Days    []digestDay   `json:"days"`
	Energy  *digestEnergy `json:"energy,omitempty"`
	Water   *digestWater  `json:"water,omitempty"`
	Air     []digestAir   `json:"air,omitempty"`
	Locks   []digestLock  `json:"locks,omitempty"`
	Entries []digestEntry `json:"entries,omitempty"`
	Rings   []digestRing  `json:"rings,omitempty"`
	BuiltAt time.Time     `json:"builtAt"`
}

// statRow is one period of a statistic: a total has change and state (the meter's reading at the end), a
// measurement has mean and max.
type statRow struct {
	Start  float64  `json:"start"` // milliseconds
	Change *float64 `json:"change"`
	State  *float64 `json:"state"`
	Mean   *float64 `json:"mean"`
	Max    *float64 `json:"max"`
}

// histRow is one state in the recorder's compressed history: the state, and when it was set.
type histRow struct {
	S  string   `json:"s"`
	LU float64  `json:"lu"` // seconds
	LC *float64 `json:"lc"` // when the state itself last changed, where that is earlier
}

func (r histRow) at() time.Time {
	s := r.LU
	if r.LC != nil {
		s = *r.LC
	}
	return time.Unix(0, int64(s*1e9))
}

const (
	digestDays  = 7
	nightEnds   = 6         // a night is midnight to six
	unlockedFor = time.Hour // a lock unlocked this long in a night, or a door open, was left that way
	digestTTL   = 30 * time.Minute
)

// midnights are the n+1 local midnights that bound the n whole days before today.
func midnights(now time.Time, loc *time.Location, n int) []time.Time {
	y, m, d := now.In(loc).Date()
	out := make([]time.Time, n+1)
	for i := range out {
		out[i] = time.Date(y, m, d-n+i, 0, 0, 0, 0, loc)
	}
	return out
}

// dayOf is the day a moment falls in, or -1.
func dayOf(bounds []time.Time, t time.Time) int {
	for i := 0; i+1 < len(bounds); i++ {
		if !t.Before(bounds[i]) && t.Before(bounds[i+1]) {
			return i
		}
	}
	return -1
}

// daily reads a figure per day from day statistics: a total's change, or (orMax, for a sensor that counts from
// midnight without being a total) the day's highest reading.
func daily(rows []statRow, bounds []time.Time, orMax bool, scale float64) []*float64 {
	out := make([]*float64, len(bounds)-1)
	for _, r := range rows {
		i := dayOf(bounds, time.UnixMilli(int64(r.Start)))
		if i < 0 {
			continue
		}
		v := r.Change
		if v == nil && orMax {
			v = r.Max
		}
		if v != nil {
			x := *v * scale
			out[i] = &x
		}
	}
	return out
}

// total adds the days that have a figure; nil when fewer than atLeast do.
func total(days []*float64, atLeast int) *float64 {
	sum, n := 0.0, 0
	for _, d := range days {
		if d != nil {
			sum += *d
			n++
		}
	}
	if n < atLeast || n == 0 {
		return nil
	}
	return &sum
}

// energyCharge is the charge across the tiers for a cumulative kWh, before the fixed charge and the tax (the
// apps' arithmetic: tariff.ts, TariffMath).
func energyCharge(tiers []house.Tier, kWh float64) float64 {
	left, from, sum := math.Max(0, kWh), 0.0, 0.0
	for _, t := range tiers {
		take := left
		if t.UpTo > 0 {
			take = math.Min(left, math.Max(0, t.UpTo-from))
			from = t.UpTo
		}
		sum += take * t.Rate
		left -= take
		if left <= 0 {
			break
		}
	}
	return sum
}

// tiersOn are the tariff's tiers on a day: the season covering it lends its own.
func tiersOn(t house.Tariff, day time.Time) []house.Tier {
	md := int(day.Month())*100 + day.Day()
	for _, s := range t.Seasons {
		var fm, fd, tm, td int
		if _, err := fmt.Sscanf(s.From, "%d-%d", &fm, &fd); err != nil {
			continue
		}
		if _, err := fmt.Sscanf(s.To, "%d-%d", &tm, &td); err != nil {
			continue
		}
		from, to := fm*100+fd, tm*100+td
		if from <= to && md >= from && md <= to || from > to && (md >= from || md <= to) {
			return s.Tiers
		}
	}
	return t.Tiers
}

// dayCharge is what one day's net kWh cost across the tiers: the charge at the period meter's reading when the
// day ended, less the charge where it began. When the meter started a new period during the day (the reading
// before does not lead to this one) the part before the reset is charged where the old period stood.
func dayCharge(tiers []house.Tier, change float64, end, before *float64) float64 {
	if end == nil {
		return energyCharge(tiers, change)
	}
	start := *end - change
	if before != nil && math.Abs(*before-start) > math.Max(1, 0.05*math.Abs(change)) {
		old := change - *end
		return energyCharge(tiers, *before+old) - energyCharge(tiers, *before) + energyCharge(tiers, *end)
	}
	return energyCharge(tiers, *end) - energyCharge(tiers, start)
}

// nightsAbove counts the nights with an hour whose average reached the limit, and the highest reading in
// those hours.
func nightsAbove(rows []statRow, bounds []time.Time, limit float64) (nights int, peak float64) {
	loc := bounds[0].Location()
	hit := make([]bool, len(bounds)-1)
	for _, r := range rows {
		t := time.UnixMilli(int64(r.Start)).In(loc)
		i := dayOf(bounds, t)
		if i < 0 || t.Hour() >= nightEnds || r.Mean == nil || *r.Mean < limit {
			continue
		}
		hit[i] = true
		top := *r.Mean
		if r.Max != nil {
			top = *r.Max
		}
		peak = math.Max(peak, top)
	}
	for _, h := range hit {
		if h {
			nights++
		}
	}
	return
}

// nightsIn counts the nights a state held for atLeast between midnight and six. The first row is the state when
// the history begins.
func nightsIn(rows []histRow, bounds []time.Time, is func(string) bool, atLeast time.Duration) int {
	loc := bounds[0].Location()
	held := make([]time.Duration, len(bounds)-1)
	for i, r := range rows {
		if !is(r.S) {
			continue
		}
		from, to := r.at(), bounds[len(bounds)-1]
		if i == 0 && from.After(bounds[0]) || from.Before(bounds[0]) {
			from = bounds[0]
		}
		if i+1 < len(rows) {
			to = rows[i+1].at()
		}
		for d := range held {
			y, m, day := bounds[d].Date()
			a, b := bounds[d], time.Date(y, m, day, nightEnds, 0, 0, 0, loc)
			if from.After(a) {
				a = from
			}
			if to.Before(b) {
				b = to
			}
			if b.After(a) {
				held[d] += b.Sub(a)
			}
		}
	}
	n := 0
	for _, h := range held {
		if h >= atLeast {
			n++
		}
	}
	return n
}

// ringCount counts a doorbell's rings in a history: an event entity's state is the time of its last event, so
// each new time within the days is a ring; a binary sensor rings each time it turns on.
func ringCount(rows []histRow, from, to time.Time) int {
	n, seen, wasOn := 0, map[string]bool{}, false
	for i, r := range rows {
		if r.S == "unavailable" || r.S == "unknown" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, r.S); err == nil {
			if !t.Before(from) && t.Before(to) && !seen[r.S] {
				seen[r.S] = true
				n++
			}
			continue
		}
		on := r.S == "on"
		if on && !wasOn && i > 0 {
			n++
		}
		wasOn = on
	}
	return n
}

// amount words a quantity: whole above a hundred, one decimal under.
func amount(v float64) string {
	if math.Abs(v) >= 100 {
		return commas(int(math.Round(v)))
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0")
}

func commas(n int) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "−" + s
	}
	return s
}

// money is the apps' wording: whole currency above twenty, cents under.
func money(v float64, currency string) string {
	a, sign := math.Abs(v), ""
	if v < 0 {
		sign = "−"
	}
	if a >= 20 {
		return sign + currency + commas(int(math.Round(a)))
	}
	return sign + currency + fmt.Sprintf("%.2f", a)
}

// against compares with the week before, when there is one.
func against(now float64, before *float64) string {
	if before == nil || *before <= 0 {
		return ""
	}
	p := (now - *before) / *before * 100
	switch {
	case math.Abs(p) < 3:
		return ", about the same as the week before"
	case p > 0:
		return fmt.Sprintf(", %.0f%% more than the week before", p)
	}
	return fmt.Sprintf(", %.0f%% less than the week before", -p)
}

func nightsWords(n int) string {
	switch {
	case n == 1:
		return "one night"
	case n >= digestDays:
		return "every night"
	}
	return fmt.Sprintf("%d nights", n)
}

// digestLines words the report.
func digestLines(r digestReport) []digestLine {
	lines := []digestLine{}
	add := func(kind string, warn bool, format string, args ...any) {
		lines = append(lines, digestLine{Kind: kind, Text: fmt.Sprintf(format, args...), Warn: warn})
	}
	if e := r.Energy; e != nil {
		if e.Used != nil {
			add("energy", false, "The house used %s kWh%s", amount(*e.Used), against(*e.Used, e.UsedBefore))
		}
		if e.Solar != nil && *e.Solar > 0 {
			add("solar", false, "Solar made %s kWh%s", amount(*e.Solar), against(*e.Solar, e.SolarBefore))
		}
		switch {
		case e.Net != nil && *e.Net < 0:
			add("cost", false, "The house sent %s kWh more to the grid than it took", amount(-*e.Net))
		case e.Cost != nil:
			add("cost", false, "Electricity came to about %s", money(*e.Cost, e.Currency))
		}
	}
	if w := r.Water; w != nil {
		add("water", false, "Water: %s%s", strings.TrimSpace(amount(w.Total)+" "+w.Unit), against(w.Total, w.Before))
	}
	high := 0
	for _, a := range r.Air {
		if a.Nights > 0 {
			high++
			add("air", true, "%s: CO₂ ran high on %s, up to %s ppm", a.Name, nightsWords(a.Nights), commas(int(math.Round(a.Peak))))
		}
	}
	if len(r.Air) > 0 && high == 0 {
		add("air", false, "CO₂ stayed in range every night")
	}
	open := 0
	for _, l := range r.Locks {
		if l.Nights > 0 {
			open++
			add("lock", true, "%s stayed unlocked on %s", l.Name, nightsWords(l.Nights))
		}
	}
	switch {
	case open > 0 || len(r.Locks) == 0:
	case len(r.Locks) == 1:
		add("lock", false, "%s was locked every night", r.Locks[0].Name)
	default:
		add("lock", false, "Every lock was locked every night")
	}
	ajar := 0
	for _, e := range r.Entries {
		if e.Nights > 0 {
			ajar++
			add("entry", true, "%s stood open on %s", e.Name, nightsWords(e.Nights))
		}
	}
	switch {
	case ajar > 0 || len(r.Entries) == 0:
	case len(r.Entries) == 1:
		add("entry", false, "%s was closed every night", r.Entries[0].Name)
	default:
		add("entry", false, "Every door was closed every night")
	}
	for _, b := range r.Rings {
		switch {
		case b.Count == 1:
			add("ring", false, "%s: one ring", b.Name)
		case b.Count > 1:
			add("ring", false, "%s: %d rings", b.Name, b.Count)
		}
	}
	return lines
}

// digestInput is what the report is worked out from: the layout, Home Assistant's states for names and units,
// and the recorder's answers.
type digestInput struct {
	House   house.House
	States  map[string]*house.State
	Bounds  []time.Time          // 2×digestDays+1 midnights: the week before, then the week
	Day     map[string][]statRow // day statistics over both weeks
	Hour    map[string][]statRow // hour statistics over the week
	History map[string][]histRow // over the week
}

func buildDigest(in digestInput, now time.Time) digestReport {
	h, both := in.House, in.Bounds
	week := both[digestDays:]
	r := digestReport{From: week[0], To: week[digestDays], Zone: week[0].Location().String(), BuiltAt: now.UTC(), Days: make([]digestDay, digestDays)}
	for i := range r.Days {
		r.Days[i].Day = week[i].Format("2006-01-02")
	}
	name := func(id string) string {
		if n := h.Names[id]; n != "" {
			return n
		}
		if st := in.States[id]; st != nil {
			if n, _ := st.Attributes["friendly_name"].(string); n != "" {
				return n
			}
		}
		return id
	}
	unit := func(id string) string {
		if st := in.States[id]; st != nil {
			u, _ := st.Attributes["unit_of_measurement"].(string)
			return u
		}
		return ""
	}
	kWh := func(id string) float64 {
		switch unit(id) {
		case "Wh":
			return 0.001
		case "MWh":
			return 1000
		}
		return 1
	}
	// A figure per day over both weeks; the first seven are the week before.
	figures := func(id string, orMax bool, scale float64) (now, before []*float64) {
		if id == "" || len(in.Day[id]) == 0 {
			return nil, nil
		}
		all := daily(in.Day[id], both, orMax, scale)
		return all[digestDays:], all[:digestDays]
	}
	// A week is compared with the one before only when both are nearly whole.
	compare := func(now, before []*float64) (*float64, *float64) {
		t := total(now, 1)
		if t == nil || total(now, digestDays-1) == nil {
			return t, nil
		}
		return t, total(before, digestDays-1)
	}

	e := digestEnergy{}
	if used, before := figures(h.Energy.UsedToday, true, kWh(h.Energy.UsedToday)); used != nil {
		e.Used, e.UsedBefore = compare(used, before)
		for i, v := range used {
			r.Days[i].Used = v
		}
	}
	if solar, before := figures(h.Energy.SolarToday, true, kWh(h.Energy.SolarToday)); solar != nil {
		e.Solar, e.SolarBefore = compare(solar, before)
		for i, v := range solar {
			r.Days[i].Solar = v
		}
	}
	if t := h.Energy.Tariff; t != nil && len(in.Day[t.Net]) > 0 {
		scale := kWh(t.Net)
		net, _ := figures(t.Net, false, scale)
		e.Net = total(net, 1)
		ends := map[int]*float64{} // the meter's reading at the end of each day of both weeks
		for _, row := range in.Day[t.Net] {
			if i := dayOf(both, time.UnixMilli(int64(row.Start))); i >= 0 && row.State != nil {
				v := *row.State * scale
				ends[i] = &v
			}
		}
		sum, days := 0.0, 0
		for i, v := range net {
			if v == nil {
				continue
			}
			c := dayCharge(tiersOn(*t, week[i]), *v, ends[digestDays+i], ends[digestDays+i-1]) * (1 + t.Tax)
			r.Days[i].Net, r.Days[i].Cost = v, &c
			sum += c
			days++
		}
		if days > 0 && len(t.Tiers) > 0 {
			sum += t.Fixed * (1 + t.Tax) * float64(days) / math.Max(1, float64(t.PeriodDays))
			e.Cost, e.Currency = &sum, t.Currency
		}
	}
	if e.Used != nil || e.Solar != nil || e.Net != nil {
		r.Energy = &e
	}

	water := h.Energy.WaterToday
	if h.Energy.Water != nil && h.Energy.Water.Today != "" {
		water = h.Energy.Water.Today
	}
	if days, before := figures(water, true, 1); days != nil {
		if t, b := compare(days, before); t != nil {
			r.Water = &digestWater{Total: *t, Before: b, Unit: unit(water)}
		}
		for i, v := range days {
			r.Days[i].Water = v
		}
	}

	if c := h.Climate; c != nil {
		limit := c.Bands["co2"].Poor
		rooms := map[string]string{}
		for _, room := range h.Rooms {
			rooms[room.ID] = room.Name
		}
		for _, a := range c.Air {
			if a.CO2 == "" || len(in.Hour[a.CO2]) == 0 || limit <= 0 {
				continue
			}
			nights, peak := nightsAbove(in.Hour[a.CO2], week, limit)
			r.Air = append(r.Air, digestAir{Room: a.Room, Name: firstNonEmpty(rooms[a.Room], a.Room), Nights: nights, Peak: peak, Limit: limit})
		}
	}
	unlocked := func(s string) bool { return s == "unlocked" || s == "open" }
	for _, id := range h.Security.Locks {
		if rows := in.History[id]; len(rows) > 0 {
			r.Locks = append(r.Locks, digestLock{Entity: id, Name: name(id), Nights: nightsIn(rows, week, unlocked, unlockedFor)})
		}
	}
	open := func(s string) bool { return s == "on" || s == "open" }
	for _, id := range h.Security.Entries {
		if rows := in.History[id]; len(rows) > 0 {
			r.Entries = append(r.Entries, digestEntry{Entity: id, Name: name(id), Nights: nightsIn(rows, week, open, unlockedFor)})
		}
	}
	for _, cam := range h.Security.Doorbells {
		if rows := in.History[h.Security.Rings[cam]]; len(rows) > 0 {
			r.Rings = append(r.Rings, digestRing{Entity: cam, Name: name(cam), Count: ringCount(rows, week[0], week[digestDays])})
		}
	}
	r.Lines = digestLines(r)
	return r
}

// digest is the week's report, worked out at most every half hour and again after midnight or a new layout.
func (g *Gateway) digest(ctx context.Context) (digestReport, error) {
	g.mu.Lock()
	cached, at, gen, fresh := g.digestReport, g.digestAt, g.generation, g.digestGen == g.generation
	g.mu.Unlock()
	if cached != nil && fresh && time.Since(at) < digestTTL && time.Now().Before(cached.To.AddDate(0, 0, 1)) {
		return *cached, nil
	}
	v, err, _ := g.building.Do("digest", func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		in, err := g.digestInput(ctx, time.Now())
		if err != nil {
			return nil, err
		}
		r := buildDigest(in, time.Now())
		g.mu.Lock()
		g.digestReport, g.digestAt, g.digestGen = &r, time.Now(), gen
		g.mu.Unlock()
		return r, nil
	})
	if err != nil {
		return digestReport{}, err
	}
	return v.(digestReport), nil
}

// digestInput asks Home Assistant: the zone and the states, then the recorder for what the layout names.
func (g *Gateway) digestInput(ctx context.Context, now time.Time) (digestInput, error) {
	in := digestInput{States: map[string]*house.State{}, Day: map[string][]statRow{}, Hour: map[string][]statRow{}, History: map[string][]histRow{}}
	b, err := g.houseJSON(ctx)
	if err != nil {
		return in, err
	}
	if err := json.Unmarshal(b.data, &in.House); err != nil {
		return in, err
	}
	first, errs, err := g.wsBatch(ctx, []map[string]any{{"type": "get_config"}, {"type": "get_states"}})
	if err != nil {
		return in, err
	}
	if errs[0] != nil || errs[1] != nil {
		return in, fmt.Errorf("%v %v", errs[0], errs[1])
	}
	g.heard()
	loc := time.Local
	if cfg, _ := first[0].(map[string]any); cfg != nil {
		if zone, _ := cfg["time_zone"].(string); zone != "" {
			if l, err := time.LoadLocation(zone); err == nil {
				loc = l
			}
		}
	}
	for _, s := range objects(first[1]) {
		id, _ := s["entity_id"].(string)
		attrs, _ := s["attributes"].(map[string]any)
		in.States[id] = &house.State{Attributes: attrs}
	}
	in.Bounds = midnights(now, loc, 2*digestDays)
	week := in.Bounds[digestDays:]
	stamp := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }

	h := in.House
	var day, hour, hist []string
	put := func(list *[]string, ids ...string) {
		for _, id := range ids {
			if id != "" && !containsFold(*list, id) {
				*list = append(*list, id)
			}
		}
	}
	put(&day, h.Energy.UsedToday, h.Energy.SolarToday, h.Energy.WaterToday)
	if h.Energy.Water != nil {
		put(&day, h.Energy.Water.Today)
	}
	if h.Energy.Tariff != nil {
		put(&day, h.Energy.Tariff.Net)
	}
	if h.Climate != nil {
		for _, a := range h.Climate.Air {
			put(&hour, a.CO2)
		}
	}
	put(&hist, h.Security.Locks...)
	put(&hist, h.Security.Entries...)
	for _, cam := range h.Security.Doorbells {
		put(&hist, h.Security.Rings[cam])
	}
	var reqs []map[string]any
	var into []any
	if len(day) > 0 {
		reqs = append(reqs, map[string]any{"type": "recorder/statistics_during_period", "start_time": stamp(in.Bounds[0]), "end_time": stamp(in.Bounds[2*digestDays]),
			"statistic_ids": day, "period": "day", "types": []string{"change", "state", "max"}})
		into = append(into, &in.Day)
	}
	if len(hour) > 0 {
		reqs = append(reqs, map[string]any{"type": "recorder/statistics_during_period", "start_time": stamp(week[0]), "end_time": stamp(week[digestDays]),
			"statistic_ids": hour, "period": "hour", "types": []string{"mean", "max"}})
		into = append(into, &in.Hour)
	}
	if len(hist) > 0 {
		reqs = append(reqs, map[string]any{"type": "history/history_during_period", "start_time": stamp(week[0]), "end_time": stamp(week[digestDays]),
			"entity_ids": hist, "minimal_response": true, "no_attributes": true, "significant_changes_only": false})
		into = append(into, &in.History)
	}
	if len(reqs) == 0 {
		return in, nil
	}
	results, errs, err := g.wsBatch(ctx, reqs)
	if err != nil {
		return in, err
	}
	failed := 0
	for i, res := range results {
		if errs[i] != nil { // a Home Assistant without the recorder still has the rest
			failed++
			continue
		}
		data, _ := json.Marshal(res)
		json.Unmarshal(data, into[i])
	}
	if failed == len(reqs) {
		return in, fmt.Errorf("the recorder did not answer: %v", errs[0])
	}
	return in, nil
}

// serveDigest answers anyone allowed in.
func (g *Gateway) serveDigest(w http.ResponseWriter, r *http.Request) {
	rep, err := g.digest(r.Context())
	if err != nil {
		http.Error(w, "Home Assistant did not answer: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(rep)
}

// digestBody is the report as a notification's text.
func digestBody(r digestReport) string {
	parts := make([]string, 0, len(r.Lines))
	for _, l := range r.Lines {
		parts = append(parts, l.Text)
	}
	return strings.Join(parts, "\n")
}
