"""MkDocs hooks for the sdmetrics docs.

Appends a content hash to every extra_css file, so a new stylesheet gets a
new URL. GitHub Pages lets browsers cache assets for ten minutes, and an
unversioned extra.css otherwise pairs fresh HTML with a stale theme.
"""

import hashlib
from pathlib import Path


def on_config(config):
    docs = Path(config["docs_dir"])
    versioned = []
    for entry in config["extra_css"]:
        path = str(entry)
        file = docs / path.split("?", 1)[0]
        if "?" not in path and file.is_file():
            digest = hashlib.sha256(file.read_bytes()).hexdigest()[:10]
            path = f"{path}?h={digest}"
        versioned.append(path)
    config["extra_css"] = versioned
    return config
