import os

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
    zed_url = os.environ.get("ZED_HOST", "http://localhost:4080") + "/api/books/_doc"
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
        requests.put(zed_url, json=data, auth=auth, timeout=(5, 30)).raise_for_status()


if __name__ == "__main__":
    main()
