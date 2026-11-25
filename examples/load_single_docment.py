import os

import requests

zed_host = os.environ.get("ZED_HOST", "http://localhost:4080")
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

res = requests.put(zed_host + "/api/" + index + "/_doc", json=data, auth=auth)
res.raise_for_status()
print(res.text)
