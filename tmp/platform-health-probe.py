#!/usr/bin/env python3
import json
import urllib.request

with urllib.request.urlopen("http://127.0.0.1:9191/health", timeout=5) as response:
    data = json.load(response)
print("status", data.get("status"))
for downstream in data.get("downstreams", []):
    if downstream.get("id") in {"platform", "host"}:
        print(downstream.get("id"), downstream.get("status"))
