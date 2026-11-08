#!/usr/bin/env python3
"""Draft an atrium.yaml from a Home Assistant's registries and states.

    python3 draft.py [--url http://homeassistant:8123] [--token TOKEN] [-o /addon_configs/df84f1fd_atrium/atrium.yaml]

Connects to the WebSocket API, reads the area, floor, device and entity registries plus every state, and writes
a DRAFT of the parts of the layout Atrium cannot draw itself (contract: mapping.md next to this script). The
rooms it does not write: Atrium draws those from the areas on every build, so a new device appears by itself.
What it drafts is the judgment: the Home readouts, Security, Energy and Music, plus a room override where an
area has more than one thermostat. Every guess carries a trailing "# check" comment: review them, then delete the
comments. Nothing in the output is invented: every entity id comes from the Home Assistant it was read from.

Token: --token, else $HA_TOKEN (a long-lived access token), else $SUPERVISOR_TOKEN (inside an add-on, where the
URL is http://supervisor/core). Standard library only: Home Assistant OS has no pip.
"""
import argparse, base64, json, os, re, socket, ssl, struct, sys, time
from urllib.parse import urlparse

# MARK: WebSocket client (RFC 6455, text frames, client-side masking; no fragments are sent by Home Assistant)

class WS:
    def __init__(self, url, timeout=60):
        u = urlparse(url)
        tls = u.scheme == 'wss'
        host, port = u.hostname, u.port or (443 if tls else 80)
        try:
            raw = socket.create_connection((host, port), timeout=timeout)
        except OSError as err:
            raise SystemExit(f"cannot connect to {host}:{port}: {err}")
        self.sock = ssl.create_default_context().wrap_socket(raw, server_hostname=host) if tls else raw
        self.f = self.sock.makefile('rb')
        key = base64.b64encode(os.urandom(16)).decode()
        path = u.path or '/'
        if u.query:
            path += '?' + u.query
        self.sock.sendall((f"GET {path} HTTP/1.1\r\nHost: {host}:{port}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                           f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n").encode())
        status = self.f.readline().decode(errors='replace').strip()
        while self.f.readline().strip():
            pass
        if ' 101 ' not in status:
            raise SystemExit(f"websocket upgrade refused: {status or 'no response'} (is this the right URL?)")
        self.next_id = 0

    def send(self, obj):
        data = json.dumps(obj).encode()
        n = len(data)
        head = b'\x81' + (bytes([0x80 | n]) if n < 126 else b'\xfe' + struct.pack('>H', n) if n < 65536 else b'\xff' + struct.pack('>Q', n))
        mask = os.urandom(4)
        self.sock.sendall(head + mask + bytes(c ^ mask[i % 4] for i, c in enumerate(data)))

    def recv(self):
        buf = b''
        while True:
            head = self.f.read(2)
            if len(head) < 2:
                raise SystemExit("Home Assistant dropped the connection")
            b0, b1 = head
            n = b1 & 0x7f
            if n == 126:
                n = struct.unpack('>H', self.f.read(2))[0]
            elif n == 127:
                n = struct.unpack('>Q', self.f.read(8))[0]
            data = self.f.read(n)
            op = b0 & 0x0f
            if op == 8:
                raise SystemExit("Home Assistant closed the connection")
            if op == 9:   # ping → pong
                self.sock.sendall(b'\x8a\x80\0\0\0\0')   # masked, empty pong
                continue
            if op == 10:
                continue
            buf += data
            if b0 & 0x80:
                return json.loads(buf)

    def call(self, msg_type, **kw):
        self.next_id += 1
        self.send({'id': self.next_id, 'type': msg_type, **kw})
        while True:
            m = self.recv()
            if m.get('id') == self.next_id and m.get('type') == 'result':
                if not m.get('success'):
                    raise SystemExit(f"{msg_type}: {m.get('error')}")
                return m.get('result')


def ws_url(url):
    u = urlparse(url if '://' in url else 'http://' + url)
    scheme = {'https': 'wss', 'wss': 'wss'}.get(u.scheme, 'ws')
    path = u.path.rstrip('/')
    if not path.endswith('/websocket'):
        path += '/websocket' if path.endswith('/core') else '/api/websocket'   # Supervisor proxy, or a plain Home Assistant
    return f"{scheme}://{u.netloc}{path}"


def fetch(url, token):
    ws = WS(ws_url(url))
    first = ws.recv()
    if first.get('type') != 'auth_required':
        raise SystemExit(f"unexpected greeting: {first}")
    ws.send({'type': 'auth', 'access_token': token})
    if ws.recv().get('type') != 'auth_ok':
        raise SystemExit("authentication failed: check the token")
    d = {}
    d['config'] = ws.call('get_config')
    d['areas'] = ws.call('config/area_registry/list')
    d['floors'] = ws.call('config/floor_registry/list')
    d['devices'] = ws.call('config/device_registry/list')
    d['entities'] = ws.call('config/entity_registry/list')
    d['states'] = ws.call('get_states')
    try:
        d['config_entries'] = ws.call('config_entries/get')   # which integrations are loaded; see restored entities below
    except SystemExit:
        d['config_entries'] = None   # an older Home Assistant: every integration is taken as loaded
    ws.sock.close()
    return d


# MARK: Layout heuristics (ported from addon/atrium/server/house/build.go)

VEHICLES = {"tesla_fleet", "tesla_custom", "teslemetry", "tessie", "toyota", "toyota_na", "bmw_connected_drive", "subaru",
            "kia_uvo", "hyundai_kia_connect", "volvo", "volvooncall", "fordpass", "mazda", "nissan_leaf", "onstar", "polestar_api",
            "audiconnect", "volkswagencarnet", "renault", "mbapi2020", "rivian", "lucidmotors", "porscheconnect"}
HAZARDS = {"smoke", "moisture", "gas", "carbon_monoxide"}
ENTRY_CLASSES = {"door", "garage_door", "opening", "window"}
ROOM_ORDER = {"light": 0, "switch": 1, "cover": 2, "climate": 3, "fan": 4, "media_player": 5, "lock": 6}


class Ent:
    __slots__ = ('id', 'domain', 'area', 'device', 'platform', 'name', 'cls', 'state', 'attrs', 'restored')


SETTLE_AFTER = 600   # seconds Home Assistant must have run before a restored entity means its device is gone


def running_for(d):
    """How long Home Assistant has been running, in seconds, read off the dump the way the add-on reads it: at start
    Home Assistant writes a restored placeholder (restored: true, unavailable) for every registered entity its
    integrations have not provided yet, so the oldest one still standing dates the start. None left: nothing to
    settle, counts as settled. Not RUNNING yet: 0."""
    if (d.get('config') or {}).get('state', 'RUNNING') != 'RUNNING':
        return 0
    oldest, restored = None, False
    for st in d['states']:
        if (st.get('attributes') or {}).get('restored'):
            restored = True
            t = parse_time(st.get('last_changed') or '')
            if t is not None and (oldest is None or t < oldest):
                oldest = t
    if not restored:
        return SETTLE_AFTER
    return 0 if oldest is None else max(0.0, time.time() - oldest)   # undated placeholders cannot date the start


def parse_time(s):
    try:
        from datetime import datetime
        return datetime.fromisoformat(s.replace('Z', '+00:00')).timestamp()
    except ValueError:
        return None


def domain(eid):
    return eid.split('.', 1)[0] if '.' in eid else ''


def pretty(eid):
    words = eid.rsplit('.', 1)[-1].replace('_', ' ').replace('-', ' ')
    return words[:1].upper() + words[1:]


def short(name, room):
    """'Family Room Floor Lamp' in Family Room → 'Floor lamp'."""
    for prefix in (room, room.replace(' Room', ''), room.replace("'s", '')):
        if name.lower().startswith(prefix.lower() + ' ') and len(name) > len(prefix) + 1:
            rest = name[len(prefix) + 1:].strip()
            return rest[:1].upper() + rest[1:]
    return name


def num(attrs, key):
    v = attrs.get(key)
    return float(v) if isinstance(v, (int, float)) and not isinstance(v, bool) else None


def heats_a_room(e):
    """A thermostat's range spans room temperature (20 °C or 70 °F); a fridge's or a freezer's does not."""
    lo, hi = num(e.attrs, 'min_temp'), num(e.attrs, 'max_temp')
    if lo is None or hi is None:
        return True
    return lo <= 20 <= hi or lo <= 70 <= hi


def is_room_temperature(e):
    try:
        v = float(e.state)
    except (TypeError, ValueError):
        return False
    if 'F' in str(e.attrs.get('unit_of_measurement', '')):
        return 50 <= v <= 95
    return 10 <= v <= 35


def build(d):
    device_area = {dev['id']: dev['area_id'] for dev in d['devices'] if dev.get('id') and dev.get('area_id')}
    registry = {e['entity_id']: e for e in d['entities'] if e.get('entity_id')}
    ents = {}
    for st in d['states']:
        eid = st['entity_id']
        r = registry.get(eid) or {}
        if r and (r.get('disabled_by') or r.get('hidden_by') or r.get('entity_category')):
            continue
        e = Ent()
        e.id, e.domain = eid, domain(eid)
        e.device = r.get('device_id') or ''
        e.area = r.get('area_id') or device_area.get(e.device, '')
        e.platform = r.get('platform') or ''
        e.state, e.attrs = st.get('state'), st.get('attributes') or {}
        e.restored = bool(e.attrs.get('restored'))
        e.name = e.attrs.get('friendly_name') or r.get('name') or r.get('original_name') or pretty(eid)
        e.cls = e.attrs.get('device_class') or r.get('device_class') or r.get('original_device_class') or ''
        ents[eid] = e
    order = sorted(ents.values(), key=lambda e: e.id)
    # Restored entities, once Home Assistant has run SETTLE_AFTER (the add-on's rule, house/auto.go): a ghost is a
    # restored entity with a live twin (same domain, same name or another device of the same name): the device came
    # back under a new id. Only an entity whose integration is loaded can be a ghost (an offline hub's entities are
    # all restored, and say nothing). Ghosts are left out; other restored entities stay in rooms, but are never a
    # guess for a slot that takes one entity. Before settling nothing is decided from them.
    settled = running_for(d) >= SETTLE_AFTER
    entries = d.get('config_entries')
    loaded = None if entries is None else {e.get('entry_id') for e in entries if e.get('state') == 'loaded'}
    entry_of = {e['entity_id']: e.get('config_entry_id') for e in d['entities'] if e.get('entity_id')}
    may_ghost = lambda e: loaded is None or not entry_of.get(e.id) or entry_of[e.id] in loaded
    device_name = {dev['id']: (dev.get('name_by_user') or dev.get('name') or '') for dev in d['devices'] if dev.get('id')}
    live_names = {(e.domain, e.name.lower()) for e in order if not e.restored}
    live_devices = {(e.domain, device_name.get(e.device, '').lower(), e.device) for e in order if not e.restored and device_name.get(e.device)}
    other_device_live = lambda e: any(dom == e.domain and name == device_name[e.device].lower() and dev != e.device for dom, name, dev in live_devices)
    ghosts = {e.id for e in order if settled and e.restored and may_ghost(e) and
              ((e.domain, e.name.lower()) in live_names or (device_name.get(e.device) and other_device_live(e)))}
    order = [e for e in order if e.id not in ghosts]
    pickable = lambda e: not (settled and e.restored)
    by_domain = lambda dom: [e for e in order if e.domain == dom]

    out = {'name': (d['config'] or {}).get('location_name') or ''}

    # Speakers: media players with device_class speaker; Music Assistant mirrors players that exist already.
    found = [e for e in by_domain('media_player') if e.cls == 'speaker']
    own = [e for e in found if e.platform != 'music_assistant']
    speakers = [e.id for e in (own or found)]
    is_speaker = set(speakers)

    # Floors by level; rooms with a floor the registry does not know go to "other".
    floors = sorted((f for f in d['floors'] if f.get('floor_id') and f.get('name')), key=lambda f: f.get('level') or 0)
    known = {f['floor_id'] for f in floors}
    floor_rank = {f['floor_id']: i for i, f in enumerate(floors)}
    out['floors'] = [{'id': f['floor_id'], 'name': f['name']} for f in floors]

    # Rooms: one per area with something to control.
    rooms, names = [], {}
    for a in d['areas']:
        aid, aname = a.get('area_id') or a.get('id'), a.get('name')
        if not aid or not aname:
            continue
        here = [e for e in order if e.area == aid and e.platform not in VEHICLES]
        ids, climates = [], []
        for e in here:
            show = False
            if e.domain == 'light' or e.domain in ('cover', 'fan', 'lock'):
                show = True
            elif e.domain == 'switch':
                show = e.platform != 'browser_mod'   # a kiosk's screen switches are not the room's
            elif e.domain == 'climate':
                if heats_a_room(e) and pickable(e):
                    climates.append(e.id)
                continue
            elif e.domain == 'media_player':
                show = e.id in is_speaker
            if show:
                ids.append(e.id)
        if not ids and not climates:
            continue
        ids.sort(key=lambda i: (ROOM_ORDER.get(domain(i), 99), ents[i].name or i))
        temp = a.get('temperature_entity_id')
        if temp not in ents or not pickable(ents[temp]):
            temp = next((e.id for e in here if e.domain == 'sensor' and e.cls == 'temperature' and pickable(e) and is_room_temperature(e)), '')
        hum = a.get('humidity_entity_id')
        if hum not in ents or not pickable(ents[hum]):
            hum = next((e.id for e in here if e.domain == 'sensor' and e.cls == 'humidity' and pickable(e)), '')
        floor = a.get('floor_id') or ''
        room = {'id': aid, 'name': aname, 'floor': floor if floor in known else 'other',
                'thermostat': climates[0] if climates else '', 'extra_climate': climates[1:],
                'temperature': temp or '', 'humidity': hum or '', 'entities': ids}
        rooms.append(room)
        for i in ids:
            if domain(i) != 'media_player':
                s = short(ents[i].name, aname)
                if s != ents[i].name:
                    names[i] = s
    if any(r['floor'] == 'other' for r in rooms):
        out['floors'].append({'id': 'other', 'name': 'Elsewhere' if floors else 'Rooms', 'check': True})
        floor_rank['other'] = len(floors)
    rooms.sort(key=lambda r: (floor_rank[r['floor']], r['name']))
    out['rooms'] = rooms

    # Scenes come by themselves; scripts do not, so they are offered here, every one a guess.
    out['scenes'] = [(e.id, True) for e in by_domain('script')][:24]

    # The whole-house readouts.
    weather = [e.id for e in by_domain('weather') if pickable(e)]
    out['home'] = {'weather': 'weather.forecast_home' if 'weather.forecast_home' in weather else (weather[0] if weather else ''),
                   'alarm': next((e.id for e in by_domain('alarm_control_panel') if pickable(e)), ''),
                   'people': [e.id for e in by_domain('person')]}

    # Security.
    cams = [e for e in by_domain('camera') if e.platform not in VEHICLES]
    bells, camera_for = [], {}
    for cam in cams:
        if not cam.device or not pickable(cam):   # the doorbell is one camera: never a restored one, once settled
            continue
        for e in order:
            if e.device == cam.device and e.domain in ('binary_sensor', 'event') and ('doorbell' in e.id or e.cls == 'doorbell'):
                bells.append(cam.id)
                break
    locks = [e.id for e in by_domain('lock') if e.platform not in VEHICLES]
    entries = [e.id for e in by_domain('cover') if e.cls in ('garage', 'gate', 'door') and e.platform not in VEHICLES]
    entries += [e.id for e in by_domain('binary_sensor') if e.cls in ENTRY_CLASSES and e.area and e.platform not in VEHICLES]
    entries = entries[:24]
    for i in entries:
        e = ents[i]
        cam = next((c for c in cams if e.device and c.device == e.device), None) or \
              next((c for c in cams if e.area and c.area == e.area), None)
        if cam:
            camera_for[i] = cam.id
    hazards = [e.id for e in by_domain('binary_sensor') if e.cls in HAZARDS and e.platform not in VEHICLES]
    out['security'] = {'cameras': [c.id for c in cams], 'doorbells': bells, 'camera_for': camera_for,
                       'locks': locks, 'entries': entries, 'hazards': hazards}

    # Energy: a best guess from names.
    powers = [e for e in by_domain('sensor') if e.cls == 'power']
    energies = [e for e in by_domain('sensor') if e.cls == 'energy' and ('today' in e.id or 'daily' in e.id)]

    def named(lst, words, not_words=()):
        for e in lst:
            n = (e.id + ' ' + e.name).lower()
            if pickable(e) and any(w in n for w in words) and not any(w in n for w in not_words):
                return e.id
        return ''
    solar = named(powers, ["solar", "pv_power", "production"])
    load = named(powers, ["consumption", "house_power", "home_power", "load_power", "main_panel"], ["solar", "production", "grid", "battery"])
    # Grid and battery powers are signed; a name alone cannot tell the sign, so the draft marks each one "check".
    grid = named(powers, ["grid_power", "grid power", "meter_power", "import_export", "net_power"], ["solar", "battery"])
    battery = named(powers, ["battery_power", "battery power", "battery_charge_discharge"], ["solar", "grid"])
    taken = {solar, load, grid, battery}
    consumers = [p.id for p in powers if p.id not in taken and p.platform not in VEHICLES][:40]
    levels = [e for e in by_domain('sensor') if e.cls == 'battery' and e.platform not in VEHICLES]
    same_device = [e for e in levels if battery and e.device and e.device == ents[battery].device]
    battery_level = (same_device[0].id if same_device else
                     named(levels, ["home_battery", "powerwall", "storage", "soc", "state_of_charge", "inverter"], ["phone", "watch", "ipad", "tablet", "remote", "lock", "sensor_battery"]))
    out['energy'] = {'solar_power': solar, 'load_power': [load] if load else [],
                     'grid_power': grid, 'battery_power': battery, 'battery_level': battery_level,
                     'solar_today': named(energies, ["solar", "production"]),
                     'used_today': named(energies, ["consumption", "house", "home", "main_panel"], ["solar", "production"]),
                     'consumers': consumers}
    out['music'] = {'speakers': speakers}
    out['names'] = names
    return out


# MARK: YAML by hand (Home Assistant OS has no PyYAML)

PLAIN = re.compile(r"^[A-Za-z0-9_][A-Za-z0-9_ .'/()\-]*$")
RESERVED = {'true', 'false', 'yes', 'no', 'on', 'off', 'null', 'y', 'n', '~'}


def scalar(s):
    s = str(s)
    if not s:
        return '""'
    looks_number = re.match(r'^[-+]?(\d[\d_]*(\.\d*)?|\.\d+)([eE][-+]?\d+)?$', s) or re.match(r'^0[xob]', s.lower())
    if PLAIN.match(s) and s.strip() == s and s.lower() not in RESERVED and not looks_number and not s.endswith(':'):
        return s
    return '"' + s.replace('\\', '\\\\').replace('"', '\\"') + '"'


def flow(ids):
    return '[' + ', '.join(scalar(i) for i in ids) + ']'


def plist(p, key, ids, comment=''):
    """A short list inline, a long one as a block (both are the same YAML)."""
    if len(ids) <= 4:
        p(f"  {key}: {flow(ids)}{comment}")
        return
    p(f"  {key}:{comment}")
    for i in ids:
        p(f"    - {scalar(i)}")


def emit(out, source):
    w = []
    p = w.append
    chk = lambda check: '   # check' if check else ''
    p(f"# atrium.yaml — DRAFT written by the atrium-mapping skill (draft.py) on {time.strftime('%Y-%m-%d')} from {source}.")
    p("# Review every '# check' and remove those comments once settled. Contract: mapping.md next to the skill (docs/mapping.md).")
    p("# Rooms are not listed: Atrium draws them from the areas. Add a room here only to change one (hide, rank, name,")
    p("# floor, thermostat, entities to borrow or drop); add a house-wide `hide:` for whole integrations or id patterns.")
    p("version: 2")
    if out['name']:
        p(f"name: {scalar(out['name'])}")
    p("")
    overrides = [r for r in out['rooms'] if r['extra_climate']]
    if overrides:
        p("rooms:")
        for r in overrides:
            p(f"  - id: {scalar(r['id'])}                 # {r['name']}")
            p(f"    thermostat: {r['thermostat']}   # check: the one that heats the room")
            p("    entities:")
            for i in r['extra_climate']:
                p(f"      - {i}   # check: a second thermostat in this area; drop it, or keep it as a control")
        p("")
    else:
        p("# rooms:                          # nothing to override: one thermostat at most per area")
        p("")
    if out['scenes']:
        p("scenes:                           # these chips first on the Home screen; every scene follows by itself")
        for eid, check in out['scenes']:
            p(f"  - entity: {eid}{chk(check)}")
        p("")
    h = out['home']
    p("home:")
    if h['weather']:
        p(f"  weather: {h['weather']}")
    p("  # outdoor_temperature: sensor.…   # a temperature sensor outside, if there is one")
    p("  # indoor_temperature: sensor.…    # a hall or whole-house temperature sensor")
    if h['alarm']:
        p(f"  alarm: {h['alarm']}")
    if h['people']:
        plist(p, 'people', h['people'])
    p("")
    s = out['security']
    if any(s.values()):
        p("security:")
        if s['cameras']:
            plist(p, 'cameras', s['cameras'])
        if s['doorbells']:
            plist(p, 'doorbells', s['doorbells'], '   # check')
        if s['camera_for']:
            p("  camera_for:                     # check: the camera to show when an entry opens")
            for k, v in s['camera_for'].items():
                p(f"    {k}: {v}")
        if s['locks']:
            plist(p, 'locks', s['locks'])
        if s['entries']:
            plist(p, 'entries', s['entries'])
        if s['hazards']:
            plist(p, 'hazards', s['hazards'])
        p("")
    else:
        p("# security:                       # nothing found: no cameras, locks, door contacts or hazard sensors")
        p("")
    e = out['energy']
    if any(e.values()):
        p("energy:                           # every line here is a guess from names: check units and signs")
        if e['solar_power']:
            p(f"  solar_power: {e['solar_power']}   # check: W, positive when producing")
        if e['load_power']:
            plist(p, 'load_power', e['load_power'], '   # check: W, positive when consuming; several are summed')
        if e['grid_power']:
            p(f"  grid_power: {e['grid_power']}   # check: signed, positive importing; if it reads positive while exporting, use {{ entity: ..., invert: true }}")
        if e['battery_power']:
            p(f"  battery_power: {e['battery_power']}   # check: signed, positive discharging; if it reads positive while charging, use {{ entity: ..., invert: true }}")
        if e['battery_level']:
            p(f"  battery_level: {e['battery_level']}   # check: percent charged")
        if not e['grid_power'] and not e['battery_power']:
            p("  # grid_power: sensor.…          # signed, positive importing; { entity: sensor.…, invert: true } flips the sign")
            p("  # battery_power: sensor.…       # signed, positive discharging")
            p("  # battery_level: sensor.…       # percent")
        if e['solar_today']:
            p(f"  solar_today: {e['solar_today']}   # check: kWh")
        if e['used_today']:
            p(f"  used_today: {e['used_today']}   # check: kWh")
        if e['consumers']:
            plist(p, 'consumers', e['consumers'], '   # check: the "Using now" list; keep the circuits worth watching')
        p("  # water_flow: sensor.…           # any unit")
        p("  # water_today: sensor.…")
        p("")
    else:
        p("# energy:                         # no power or energy sensors found")
        p("")
    if out['music']['speakers']:
        p("music:")
        plist(p, 'speakers', out['music']['speakers'])
        p("")
    else:
        p("# music:                          # no media_player with device_class speaker")
        p("")
    return '\n'.join(w).rstrip('\n') + '\n'


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--url', help="Home Assistant's URL (default http://homeassistant:8123; inside an add-on http://supervisor/core)")
    ap.add_argument('--token', help="access token (default $HA_TOKEN, then $SUPERVISOR_TOKEN)")
    ap.add_argument('-o', '--output', help="write here instead of stdout")
    args = ap.parse_args()
    token = args.token or os.environ.get('HA_TOKEN') or os.environ.get('SUPERVISOR_TOKEN')
    if not token:
        raise SystemExit("no token: pass --token, or set HA_TOKEN (a long-lived access token) or SUPERVISOR_TOKEN")
    url = args.url or ('http://supervisor/core' if not os.environ.get('HA_TOKEN') and os.environ.get('SUPERVISOR_TOKEN') else 'http://homeassistant:8123')
    d = fetch(url, token)
    out = build(d)
    text = emit(out, out['name'] or url)
    if args.output:
        with open(args.output, 'w', encoding='utf-8') as f:
            f.write(text)
    else:
        sys.stdout.write(text)
    s, e = out['security'], out['energy']
    print(f"draft: {len(out['rooms'])} rooms seen ({sum(1 for r in out['rooms'] if r['extra_climate'])} overrides suggested), "
          f"{len(out['scenes'])} scripts offered as scenes, "
          f"{len(s['cameras'])} cameras ({len(s['doorbells'])} doorbells), {len(s['locks'])} locks, {len(s['entries'])} entries, "
          f"{len(s['hazards'])} hazards, {len(e['consumers'])} consumers, {len(out['music']['speakers'])} speakers; "
          f"{len(d['states'])} states read", file=sys.stderr)


if __name__ == '__main__':
    main()
