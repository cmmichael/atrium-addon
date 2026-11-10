package main

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cmmichael/atrium/addon/atrium/server/house"
)

func f(v float64) *float64 { return &v }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func losAngeles(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestMidnightsFollowTheHousesClock(t *testing.T) {
	loc := losAngeles(t)
	// Monday 9 November 2026, 02:30 UTC is still Sunday evening in Los Angeles; the week before holds the
	// 25-hour day the clocks went back (1 November).
	now := time.Date(2026, 11, 9, 2, 30, 0, 0, time.UTC)
	b := midnights(now, loc, 7)
	if len(b) != 8 || b[7].Format("2006-01-02 15:04") != "2026-11-08 00:00" || b[0].Format("2006-01-02") != "2026-11-01" {
		t.Fatalf("bounds %v", b)
	}
	if d := b[1].Sub(b[0]); d != 25*time.Hour {
		t.Errorf("the day the clocks went back is %v long", d)
	}
	if dayOf(b, b[0]) != 0 || dayOf(b, b[7]) != -1 || dayOf(b, b[7].Add(-time.Second)) != 6 || dayOf(b, b[0].Add(-time.Second)) != -1 {
		t.Error("dayOf is off at the edges")
	}
}

func TestDailyTakesTheChangeAndOnlyThenTheMax(t *testing.T) {
	loc := losAngeles(t)
	b := midnights(time.Date(2026, 11, 9, 12, 0, 0, 0, loc), loc, 3)
	ms := func(i int) float64 { return float64(b[i].UnixMilli()) }
	rows := []statRow{{Start: ms(0), Change: f(12)}, {Start: ms(1), Max: f(9)}, {Start: ms(2) + 5, Change: f(-2), Max: f(40)}, {Start: ms(3), Change: f(99)}}
	got := daily(rows, b, true, 0.001)
	if got[0] == nil || !near(*got[0], 0.012) || got[1] == nil || !near(*got[1], 0.009) || got[2] == nil || !near(*got[2], -0.002) {
		t.Errorf("daily %v %v %v", got[0], got[1], got[2])
	}
	if strict := daily(rows, b, false, 1); strict[1] != nil {
		t.Error("a meter without a change took its highest reading")
	}
	if total(got, 4) != nil || !near(*total(got, 3), 0.019) || total([]*float64{nil, nil}, 0) != nil {
		t.Error("total")
	}
}

func TestChargesFollowTheTiers(t *testing.T) {
	tiers := []house.Tier{{UpTo: 100, Rate: 0.10}, {UpTo: 300, Rate: 0.20}, {Rate: 0.40}}
	for kWh, want := range map[float64]float64{-5: 0, 50: 5, 100: 10, 250: 40, 400: 90} {
		if got := energyCharge(tiers, kWh); !near(got, want) {
			t.Errorf("%v kWh: %v, want %v", kWh, got, want)
		}
	}
	// A day that crossed from the first tier into the second: 90 → 120.
	if got := dayCharge(tiers, 30, f(120), f(90)); !near(got, 1+4) {
		t.Errorf("crossing a tier: %v", got)
	}
	// No reading: the day stands alone in the first tier.
	if got := dayCharge(tiers, 30, nil, nil); !near(got, 3) {
		t.Errorf("without a reading: %v", got)
	}
	// The period ended during the day: the meter stood at 290, the day took 30, and it ends at 12. The first 18
	// were charged at 290 (10 in the second tier, 8 in the third), the 12 after from nothing.
	if got := dayCharge(tiers, 30, f(12), f(290)); !near(got, 10*0.20+8*0.40+12*0.10) {
		t.Errorf("across a reset: %v", got)
	}
	// A day the house sent more than it took, low in the first tier: the credit stops at nothing.
	if got := dayCharge(tiers, -20, f(-15), f(5)); !near(got, -0.5) {
		t.Errorf("a day of sending: %v", got)
	}
}

func TestTiersOnASeason(t *testing.T) {
	summer := []house.Tier{{Rate: 0.5}}
	winter := []house.Tier{{Rate: 0.3}}
	tariff := house.Tariff{Tiers: []house.Tier{{Rate: 0.1}}, Seasons: []house.TariffSeason{
		{Name: "Summer", From: "06-01", To: "09-30", Tiers: summer}, {Name: "Winter", From: "11-15", To: "02-28", Tiers: winter}, {Name: "odd", From: "soon", To: "later", Tiers: summer}}}
	on := func(m time.Month, d int) float64 {
		return tiersOn(tariff, time.Date(2026, m, d, 0, 0, 0, 0, time.UTC))[0].Rate
	}
	if on(7, 4) != 0.5 || on(9, 30) != 0.5 || on(10, 1) != 0.1 || on(12, 25) != 0.3 || on(1, 3) != 0.3 || on(3, 1) != 0.1 {
		t.Error("seasons")
	}
}

func TestNightsOfPoorAir(t *testing.T) {
	loc := losAngeles(t)
	b := midnights(time.Date(2026, 11, 9, 12, 0, 0, 0, loc), loc, 7)
	at := func(day, hour int) float64 { return float64(b[day].Add(time.Duration(hour) * time.Hour).UnixMilli()) }
	rows := []statRow{
		{Start: at(0, 2), Mean: f(1500), Max: f(1650)}, {Start: at(0, 3), Mean: f(1450), Max: f(1500)}, // one night, two hours
		{Start: at(2, 14), Mean: f(2000), Max: f(2100)}, // an afternoon is not a night
		{Start: at(4, 5), Mean: f(1400)},                // just at the band, no max recorded
		{Start: at(5, 6), Mean: f(1900)},                // six o'clock is morning
		{Start: at(6, 1), Mean: f(1399), Max: f(1800)},  // a spike in an hour that averaged under
	}
	if n, peak := nightsAbove(rows, b, 1400); n != 2 || peak != 1650 {
		t.Errorf("%d nights, peak %v", n, peak)
	}
}

func TestNightsALockStoodUnlocked(t *testing.T) {
	loc := losAngeles(t)
	b := midnights(time.Date(2026, 11, 9, 12, 0, 0, 0, loc), loc, 7)
	row := func(state string, day int, at time.Duration) histRow {
		return histRow{S: state, LU: float64(b[day].Add(at).Unix())}
	}
	unlocked := func(s string) bool { return s == "unlocked" }
	rows := []histRow{
		{S: "unlocked", LU: float64(b[0].Add(-3 * time.Hour).Unix())}, // unlocked since the evening before the week
		row("locked", 0, 90*time.Minute),                              // …until 01:30: a night
		row("unlocked", 1, 5*time.Hour+30*time.Minute),                // 05:30 to 09:00: half an hour of night
		row("locked", 1, 9*time.Hour),
		row("unlocked", 3, 22*time.Hour), // from ten in the evening through the next night
		row("unavailable", 4, 3*time.Hour),
		row("locked", 4, 4*time.Hour),
		row("unlocked", 6, 23*time.Hour), // to the end of the week, none of it a night inside it
	}
	if n := nightsIn(rows, b, unlocked, time.Hour); n != 2 {
		t.Errorf("%d nights", n)
	}
	// Unlocked all week: every night, from a single row older than the week.
	if n := nightsIn(rows[:1], b, unlocked, time.Hour); n != 7 {
		t.Errorf("all week: %d nights", n)
	}
	// lc is when the state began, where an attribute changed later.
	lc := float64(b[2].Add(time.Hour).Unix())
	late := []histRow{row("locked", 0, 0), {S: "unlocked", LU: float64(b[2].Add(8 * time.Hour).Unix()), LC: &lc}, row("locked", 2, 9*time.Hour)}
	if n := nightsIn(late, b, unlocked, time.Hour); n != 1 {
		t.Errorf("by lc: %d nights", n)
	}
}

func TestRings(t *testing.T) {
	loc := losAngeles(t)
	b := midnights(time.Date(2026, 11, 9, 12, 0, 0, 0, loc), loc, 7)
	stamp := func(day int, at time.Duration) string {
		return b[day].Add(at).UTC().Format("2006-01-02T15:04:05.000+00:00")
	}
	event := []histRow{{S: stamp(0, -time.Hour)}, {S: "unavailable"}, {S: stamp(0, -time.Hour)}, {S: stamp(1, time.Hour)}, {S: stamp(1, time.Hour)}, {S: stamp(5, 2*time.Hour)}, {S: "unknown"}}
	if n := ringCount(event, b[0], b[7]); n != 2 {
		t.Errorf("event entity: %d rings", n)
	}
	binary := []histRow{{S: "on"}, {S: "off"}, {S: "on"}, {S: "unavailable"}, {S: "on"}, {S: "off"}, {S: "on"}}
	if n := ringCount(binary, b[0], b[7]); n != 2 {
		t.Errorf("binary sensor: %d rings", n)
	}
}

func TestWords(t *testing.T) {
	for got, want := range map[string]string{
		amount(212.4): "212", amount(1650): "1,650", amount(48.26): "48.3", amount(7): "7",
		money(74.4, "$"): "$74", money(12.5, "€"): "€12.50", money(-3, "$"): "−$3.00", money(1234.5, "$"): "$1,235",
		against(88, f(100)): ", 12% less than the week before", against(110, f(100)): ", 10% more than the week before",
		against(101, f(100)): ", about the same as the week before", against(5, nil): "", against(5, f(0)): "",
		nightsWords(1): "one night", nightsWords(3): "3 nights", nightsWords(7): "every night",
	} {
		if got != want {
			t.Errorf("%q, want %q", got, want)
		}
	}
}

// A whole house over two weeks, as the recorder answers.
func TestBuildDigest(t *testing.T) {
	loc := losAngeles(t)
	now := time.Date(2026, 11, 9, 9, 0, 0, 0, loc)
	b := midnights(now, loc, 14)
	ms := func(i int) float64 { return float64(b[i].UnixMilli()) }
	var used, solar, net, water []statRow
	reading := 181.4
	for i := 0; i < 14; i++ {
		u, s := 30.0, 20.0
		if i < 7 {
			u, s = 34, 20.2
		}
		used = append(used, statRow{Start: ms(i), Max: f(u * 1000)}) // a Wh sensor that counts from midnight
		solar = append(solar, statRow{Start: ms(i), Change: f(s)})
		reading += u - s
		net = append(net, statRow{Start: ms(i), Change: f(u - s), State: f(reading)})
		if i != 9 { // a day the water meter was away
			water = append(water, statRow{Start: ms(i), Change: f(110)})
		}
	}
	night := func(day, hour int) float64 { return float64(b[7+day].Add(time.Duration(hour) * time.Hour).UnixMilli()) }
	in := digestInput{
		House: house.House{
			Rooms: []house.Room{{ID: "bedroom", Name: "Bedroom"}, {ID: "office", Name: "Office"}},
			Names: map[string]string{"lock.front": "Front door"},
			Energy: house.Energy{UsedToday: "sensor.used", SolarToday: "sensor.solar", WaterToday: "sensor.water",
				Tariff: &house.Tariff{Net: "sensor.net", PeriodDays: 30, Fixed: 15, Tax: 0.1, Currency: "$", Tiers: []house.Tier{{UpTo: 300, Rate: 0.30}, {Rate: 0.50}}}},
			Climate:  &house.Climate{Air: []house.Air{{Room: "bedroom", CO2: "sensor.bed_co2"}, {Room: "office", CO2: "sensor.office_co2"}, {Room: "attic", CO2: "sensor.gone"}}, Bands: map[string]house.Band{"co2": {Fair: 1000, Poor: 1400}}},
			Security: house.Security{Entries: []string{"cover.garage", "binary_sensor.side_door"}, Locks: []string{"lock.front", "lock.back"}, Doorbells: []string{"camera.door"}, Rings: map[string]string{"camera.door": "event.door_ring"}},
		},
		States: map[string]*house.State{
			"sensor.used":  {Attributes: map[string]any{"unit_of_measurement": "Wh"}},
			"sensor.water": {Attributes: map[string]any{"unit_of_measurement": "gal"}},
			"lock.back":    {Attributes: map[string]any{"friendly_name": "Back door"}},
			"camera.door":  {Attributes: map[string]any{"friendly_name": "Front doorbell"}},
		},
		Bounds: b,
		Day:    map[string][]statRow{"sensor.used": used, "sensor.solar": solar, "sensor.net": net, "sensor.water": water},
		Hour: map[string][]statRow{
			"sensor.bed_co2":    {{Start: night(1, 3), Mean: f(1500), Max: f(1648.6)}, {Start: night(4, 2), Mean: f(1420), Max: f(1500)}},
			"sensor.office_co2": {{Start: night(1, 3), Mean: f(700), Max: f(900)}},
		},
		History: map[string][]histRow{
			"lock.front":              {{S: "locked", LU: float64(b[7].Unix())}, {S: "unlocked", LU: float64(b[9].Add(-time.Hour).Unix())}, {S: "locked", LU: float64(b[9].Add(7 * time.Hour).Unix())}},
			"lock.back":               {{S: "locked", LU: float64(b[7].Unix())}},
			"cover.garage":            {{S: "closed", LU: float64(b[7].Unix())}, {S: "open", LU: float64(b[10].Add(-2 * time.Hour).Unix())}, {S: "closed", LU: float64(b[11].Add(8 * time.Hour).Unix())}},
			"binary_sensor.side_door": {{S: "off", LU: float64(b[7].Unix())}, {S: "on", LU: float64(b[8].Add(3 * time.Hour).Unix())}, {S: "off", LU: float64(b[8].Add(3*time.Hour + time.Minute).Unix())}},
			"event.door_ring":         {{S: "unknown"}, {S: b[8].Add(10 * time.Hour).UTC().Format(time.RFC3339Nano)}, {S: b[10].Add(15 * time.Hour).UTC().Format(time.RFC3339Nano)}, {S: b[12].UTC().Format(time.RFC3339Nano)}},
		},
	}
	r := buildDigest(in, now)
	if r.From != b[7] || r.To != b[14] || r.Zone != "America/Los_Angeles" || len(r.Days) != 7 || r.Days[0].Day != "2026-11-02" || r.Days[6].Day != "2026-11-08" {
		t.Fatalf("the week: %v to %v, %d days", r.From, r.To, len(r.Days))
	}
	// 7 days of 10 kWh net from a reading of 278: 22 in the first tier, 48 in the second, then the tax, and a
	// week of the fixed charge.
	cost := (22*0.30+48*0.50)*1.1 + 15*1.1*7/30
	if r.Energy == nil || !near(*r.Energy.Used, 210) || !near(*r.Energy.UsedBefore, 238) || !near(*r.Energy.Net, 70) || !near(*r.Energy.Cost, cost) {
		t.Fatalf("energy %+v", r.Energy)
	}
	if !near(*r.Days[2].Cost, (2*0.30+8*0.50)*1.1) || !near(*r.Days[0].Used, 30) || !near(*r.Days[0].Net, 10) {
		t.Errorf("the day the tier was crossed: %+v", r.Days[2])
	}
	// The week has six days of water, the one before seven: compared all the same.
	if r.Water == nil || r.Water.Total != 660 || r.Water.Before == nil || *r.Water.Before != 770 || r.Days[2].Water != nil {
		t.Errorf("water %+v", r.Water)
	}
	want := []string{
		"The house used 210 kWh, 12% less than the week before",
		"Solar made 140 kWh, about the same as the week before",
		"Electricity came to about $38",
		"Water: 660 gal, 14% less than the week before",
		"Bedroom: CO₂ ran high on 2 nights, up to 1,649 ppm",
		"Front door stayed unlocked on one night",
		"cover.garage stood open on 2 nights",
		"Front doorbell: 3 rings",
	}
	var got []string
	for _, l := range r.Lines {
		got = append(got, l.Text)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !r.Lines[4].Warn || !r.Lines[5].Warn || r.Lines[0].Warn || r.Lines[4].Kind != "air" {
		t.Error("what is worth a look")
	}
	if len(r.Air) != 2 || len(r.Locks) != 2 || r.Locks[1].Name != "Back door" || r.Locks[1].Nights != 0 {
		t.Errorf("air %+v locks %+v", r.Air, r.Locks)
	}
	if body := digestBody(r); strings.Count(body, "\n") != 7 {
		t.Errorf("body %q", body)
	}

	// A quiet house with nothing recorded says nothing; one with good nights says so.
	if quiet := buildDigest(digestInput{Bounds: b}, now); len(quiet.Lines) != 0 || quiet.Lines == nil || quiet.Energy != nil {
		t.Errorf("an empty house: %+v", quiet.Lines)
	}
	in.Hour["sensor.bed_co2"] = in.Hour["sensor.office_co2"]
	in.History["lock.front"] = in.History["lock.back"]
	in.History["cover.garage"] = in.History["cover.garage"][:1]
	in.Day["sensor.net"] = []statRow{{Start: ms(8), Change: f(-32)}}
	good := buildDigest(in, now)
	var words []string
	for _, l := range good.Lines {
		words = append(words, l.Text)
	}
	all := strings.Join(words, "\n")
	for _, s := range []string{"CO₂ stayed in range every night", "Every lock was locked every night", "Every door was closed every night", "The house sent 32 kWh more to the grid than it took"} {
		if !strings.Contains(all, s) {
			t.Errorf("missing %q in:\n%s", s, all)
		}
	}
}
