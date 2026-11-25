import os

import requests

zed_host = os.environ.get("ZED_HOST", "http://localhost:4080")
auth = (os.environ.get("ZED_USER", "admin"), os.environ.get("ZED_PASSWORD", "Complexpass#123"))
index = "games3"

# search_type: match, matchphrase, term, prefix, wildcard, fuzzy, querystring,
# matchall; optional query.start_time / query.end_time filter on @timestamp.
params = {
    "search_type": "match",
    "query": {"term": "DEMTSCHENKO", "field": "_all"},
    "max_results": 10,
}

# params = {
#     "search_type": "querystring",
#     "query": {"term": "+City:Turin +Silver"},
# }

res = requests.post(zed_host + "/api/" + index + "/_search", json=params, auth=auth, timeout=(5, 30))
res.raise_for_status()
print(res.text)
