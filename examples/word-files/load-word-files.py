import os
import sys
from urllib.parse import quote, urlparse

import docx
import requests
from docx.table import Table

folder = os.path.join(os.path.dirname(os.path.abspath(__file__)), "sample-files")


def text_of(container):
    """Paragraph and table-cell text of a document or cell in body order, nested tables included."""
    lines = []
    for item in container.iter_inner_content():
        if not isinstance(item, Table):
            lines.append(item.text)
            continue
        seen = set()  # a merged cell repeats in every row and column it spans
        for row in item.rows:
            for cell in row.cells:
                if cell._tc not in seen:
                    seen.add(cell._tc)
                    lines.append(text_of(cell))
    return "\n".join(lines)


def main():
    zed_host = os.environ.get("ZED_HOST", "http://localhost:4080")
    # Basic auth would cross the network in cleartext over http:// to another host.
    if urlparse(zed_host).scheme != "https" and urlparse(zed_host).hostname not in ("localhost", "127.0.0.1", "::1"):
        sys.exit("ZED_HOST must use https:// for a remote host")
    zed_url = zed_host + "/api/books/_doc"
    auth = (os.environ.get("ZED_USER", "admin"), os.environ.get("ZED_PASSWORD", "Complexpass#123"))

    for file in sorted(os.listdir(folder)):
        if not file.endswith(".docx"):
            continue
        print(file)
        doc = docx.Document(os.path.join(folder, file))
        data = {
            "file": file,
            "text": text_of(doc),
        }
        # the file name is the id, so a rerun updates the documents instead of adding copies;
        # a redirect is refused: it could resend the text over http://
        res = requests.put(zed_url + "/" + quote(file, safe=""), json=data, auth=auth, timeout=(5, 30), allow_redirects=False)
        if res.is_redirect:
            sys.exit(f"{file}: ZED_HOST answered with a redirect, the document was not sent again")
        res.raise_for_status()


if __name__ == "__main__":
    main()
