"""Tests for draft.py's guesses, run with: python3 -m unittest skills/atrium-mapping/draft_test.py"""
import importlib.util
import os
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location('draft', os.path.join(HERE, 'draft.py'))
draft = importlib.util.module_from_spec(spec)
spec.loader.exec_module(draft)


def sensor(eid, name, cls, state='1'):
    return {'entity_id': eid, 'state': state, 'attributes': {'friendly_name': name, 'device_class': cls}}


def dump(states, **more):
    d = {'config': {'location_name': 'Test', 'state': 'RUNNING'}, 'areas': [], 'floors': [], 'devices': [], 'entities': [], 'states': states}
    d.update(more)
    return d


def restored(eid, name, cls='', ago=1200):
    import datetime
    t = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(seconds=ago)
    attrs = {'friendly_name': name, 'restored': True}
    if cls:
        attrs['device_class'] = cls
    return {'entity_id': eid, 'state': 'unavailable', 'attributes': attrs, 'last_changed': t.isoformat()}


class EnergyGuesses(unittest.TestCase):
    def test_grid_and_battery_are_guessed_and_marked_check(self):
        out = draft.build(dump([
            sensor('sensor.pv_power', 'PV power', 'power'),
            sensor('sensor.house_power', 'House power', 'power'),
            sensor('sensor.grid_power', 'Grid power', 'power'),
            sensor('sensor.battery_power', 'Battery power', 'power'),
            sensor('sensor.powerwall_charge', 'Powerwall charge', 'battery', '64'),
            sensor('sensor.oven_power', 'Oven power', 'power'),
        ]))
        e = out['energy']
        self.assertEqual(e['solar_power'], 'sensor.pv_power')
        self.assertEqual(e['load_power'], ['sensor.house_power'])
        self.assertEqual(e['grid_power'], 'sensor.grid_power')
        self.assertEqual(e['battery_power'], 'sensor.battery_power')
        self.assertEqual(e['battery_level'], 'sensor.powerwall_charge')
        self.assertEqual(e['consumers'], ['sensor.oven_power'], 'grid and battery sensors are not consumers')
        text = draft.emit(out, 'test')
        for line in text.splitlines():
            if line.startswith(('  grid_power:', '  battery_power:', '  battery_level:')):
                self.assertIn('# check', line, line)
        self.assertIn('  grid_power: sensor.grid_power', text)
        self.assertIn('invert: true', text, 'the draft shows how to flip a sign')

    def test_a_house_without_a_battery_gets_commented_examples(self):
        out = draft.build(dump([sensor('sensor.pv_power', 'PV power', 'power'), sensor('sensor.house_power', 'House power', 'power')]))
        e = out['energy']
        self.assertEqual((e['grid_power'], e['battery_power'], e['battery_level']), ('', '', ''))
        text = draft.emit(out, 'test')
        self.assertIn('  # grid_power:', text)
        self.assertIn('  # battery_power:', text)
        self.assertNotIn('\n  grid_power:', text)

    def test_a_phone_battery_is_not_the_house_battery(self):
        out = draft.build(dump([sensor('sensor.house_power', 'House power', 'power'), sensor('sensor.phone_battery_level', 'Phone battery level', 'battery', '80')]))
        self.assertEqual(out['energy']['battery_level'], '')


class RestoredEntities(unittest.TestCase):
    def house(self, ago):
        light = lambda eid, name: {'entity_id': eid, 'state': 'on', 'attributes': {'friendly_name': name}}
        return dump(
            [light('light.kitchen_main', 'Kitchen Main lights'), restored('light.kitchen_old', 'Kitchen Main lights', ago=ago),
             restored('light.kitchen_winter', 'Winter lamp', ago=ago),
             {'entity_id': 'camera.front', 'state': 'idle', 'attributes': {'friendly_name': 'Front'}},
             {'entity_id': 'binary_sensor.front_doorbell', 'state': 'off', 'attributes': {'device_class': 'doorbell'}},
             restored('camera.garage', 'Garage', ago=ago),
             {'entity_id': 'binary_sensor.garage_doorbell', 'state': 'off', 'attributes': {'device_class': 'doorbell'}},
             restored('sensor.kitchen_old_temperature', 'Old kitchen temperature', 'temperature', ago=ago),
             sensor('sensor.kitchen_temperature', 'Kitchen temperature', 'temperature', '21.5'),
             restored('weather.old', 'Old weather', ago=ago), {'entity_id': 'weather.home', 'state': 'sunny', 'attributes': {}}],
            areas=[{'area_id': 'kitchen', 'name': 'Kitchen'}],
            devices=[{'id': 'bell', 'name': 'Front'}, {'id': 'bell2', 'name': 'Garage'}],
            entities=[{'entity_id': 'light.kitchen_main', 'area_id': 'kitchen'}, {'entity_id': 'light.kitchen_old', 'area_id': 'kitchen'},
                      {'entity_id': 'light.kitchen_winter', 'area_id': 'kitchen'}, {'entity_id': 'sensor.kitchen_old_temperature', 'area_id': 'kitchen'},
                      {'entity_id': 'sensor.kitchen_temperature', 'area_id': 'kitchen'},
                      {'entity_id': 'camera.front', 'device_id': 'bell'}, {'entity_id': 'binary_sensor.front_doorbell', 'device_id': 'bell'},
                      {'entity_id': 'camera.garage', 'device_id': 'bell2'}, {'entity_id': 'binary_sensor.garage_doorbell', 'device_id': 'bell2'}])

    def test_before_settling_nothing_is_decided_from_restored_entities(self):
        d = self.house(ago=60)
        self.assertLess(draft.running_for(d), draft.SETTLE_AFTER)
        out = draft.build(d)
        self.assertEqual(out['rooms'][0]['entities'], ['light.kitchen_main', 'light.kitchen_old', 'light.kitchen_winter'])
        self.assertEqual(out['security']['doorbells'], ['camera.front', 'camera.garage'])

    def test_once_settled_ghosts_go_and_restored_entities_are_never_a_guess(self):
        d = self.house(ago=1200)
        self.assertGreaterEqual(draft.running_for(d), draft.SETTLE_AFTER)
        out = draft.build(d)
        room = out['rooms'][0]
        self.assertEqual(room['entities'], ['light.kitchen_main', 'light.kitchen_winter'], 'the ghost goes, the lone restored lamp stays')
        self.assertEqual(room['temperature'], 'sensor.kitchen_temperature')
        self.assertEqual(out['security']['doorbells'], ['camera.front'])
        self.assertIn('camera.garage', out['security']['cameras'], 'a restored camera with no twin stays listed')
        self.assertEqual(out['home']['weather'], 'weather.home')

    def test_a_dead_sibling_on_its_own_device_is_not_a_ghost(self):
        d = self.house(ago=1200)
        d['devices'].append({'id': 'strip', 'name': 'Kitchen Strip'})
        d['entities'] += [{'entity_id': 'switch.strip_1', 'area_id': 'kitchen', 'device_id': 'strip'}, {'entity_id': 'switch.strip_3', 'area_id': 'kitchen', 'device_id': 'strip'}]
        d['states'] += [{'entity_id': 'switch.strip_1', 'state': 'on', 'attributes': {'friendly_name': 'Strip outlet 1'}}, restored('switch.strip_3', 'Strip outlet 3')]
        self.assertIn('switch.strip_3', draft.build(d)['rooms'][0]['entities'])

    def test_an_offline_integrations_entities_are_not_ghosts(self):
        d = self.house(ago=1200)
        d['config_entries'] = [{'entry_id': 'hue', 'state': 'setup_retry'}, {'entry_id': 'zha', 'state': 'loaded'}]
        for e in d['entities']:
            e['config_entry_id'] = 'hue' if e['entity_id'] == 'light.kitchen_old' else 'zha'
        self.assertIn('light.kitchen_old', draft.build(d)['rooms'][0]['entities'], 'the Hue bridge is down, not replaced')
        d['config_entries'][0]['state'] = 'loaded'
        self.assertNotIn('light.kitchen_old', draft.build(d)['rooms'][0]['entities'])

    def test_undated_restored_entities_are_not_settled(self):
        d = self.house(ago=1200)
        for st in d['states']:
            st.pop('last_changed', None)
        self.assertEqual(draft.running_for(d), 0)

    def test_not_running_is_never_settled(self):
        d = self.house(ago=1200)
        d['config']['state'] = 'STARTING'
        self.assertEqual(draft.running_for(d), 0)

    def test_a_house_without_restored_entities_counts_as_settled(self):
        self.assertEqual(draft.running_for(dump([sensor('sensor.house_power', 'House power', 'power')])), draft.SETTLE_AFTER)


if __name__ == '__main__':
    unittest.main()
