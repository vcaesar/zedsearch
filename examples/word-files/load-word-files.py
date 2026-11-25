import os

import docx
import requests

folder = os.path.join(os.path.dirname(os.path.abspath(__file__)), "sample-files")


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
            "text": "\n".join(p.text for p in doc.paragraphs),
        }
        requests.put(zed_url, json=data, auth=auth).raise_for_status()


if __name__ == "__main__":
    main()
