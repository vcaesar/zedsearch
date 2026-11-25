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

# a redirect is refused: it could resend the body over http://
res = requests.post(zed_host + "/api/" + index + "/_search", json=params, auth=auth, timeout=(5, 30), allow_redirects=False)
if res.is_redirect:
    sys.exit("ZED_HOST answered with a redirect, the request was not sent again")
res.raise_for_status()
print(res.text)
