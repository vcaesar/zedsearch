import os
import sys
from urllib.parse import urlparse

import requests

zed_host = os.environ.get("ZED_HOST", "http://localhost:4080")

# Basic auth would cross the network in cleartext over http:// to another host.
if urlparse(zed_host).scheme != "https" and urlparse(zed_host).hostname not in ("localhost", "127.0.0.1", "::1"):
    sys.exit("ZED_HOST must use https:// for a remote host")
auth = (os.environ.get("ZED_USER", "admin"), os.environ.get("ZED_PASSWORD", "Complexpass#123"))
index = "games3"

data = {
    "Athlete": "DEMTSCHENKO, Albert",
    "City": "Turin",
    "Country": "RUS",
    "Discipline": "Luge",
    "Event": "Singles",
    "Gender": "Men",
    "Medal": "Silver",
    "Season": "winter",
    "Sport": "Luge",
    "Year": 2006,
}

res = requests.put(zed_host + "/api/" + index + "/_doc", json=data, auth=auth, timeout=(5, 30))
res.raise_for_status()
print(res.text)
