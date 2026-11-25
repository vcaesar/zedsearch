# Load word files

`load-word-files.py` parses every `.docx` file in `sample-files/` and indexes it into the `books` index, one document per file with the fields `file` and `text` (paragraphs and table cells, nested tables included, in document order).

```sh
pip install 'python-docx>=1.1.2' requests
python3 load-word-files.py
```

`ZED_HOST` (default `http://localhost:4080`), `ZED_USER` (default `admin`) and `ZED_PASSWORD` (default `Complexpass#123`) select another server or credentials; a remote `ZED_HOST` must use `https://`. To load your own files, change `folder` in the script.
