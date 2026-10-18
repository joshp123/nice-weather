# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Exact named-coordinate target clocks exported from the shared 1 km model."""
import json
import math


class TargetTimezones:
    def __init__(self, path):
        with open(path) as stream:
            data = json.load(stream)
        self.generation = data['generation']
        self.names = data['timezoneNames']
        self.zones = {(lat, lon): zone for lat, lon, zone in data['coordinates']}

    def timezone_at(self, lat, lng):
        # Match JavaScript Math.round, including negative coordinates.
        key = (math.floor(lat*100000+.5), math.floor(lng*100000+.5))
        zone = self.zones[key]
        return None if zone is None else self.names[zone]
