#!/usr/bin/env -S uv run --script
"""Convert CRD openAPIV3Schema to JSON Schema files for kubeconform.

Usage: crd_to_jsonschema.py <crd-dir> <out-dir>

Layout matches kubeconform's -schema-location template
'{{ .Group }}/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json'
(same convention as the datree CRDs-catalog), so the rendered chart and
the gitops/ seed manifests validate against the CRDs this very commit
ships — schema drift between chart and governance objects fails the
pipeline instead of ArgoCD.
"""
# Inline metadata (PEP 723): `uv run` resolves pyyaml into an ephemeral
# env on its own — no venv, no `pip install` step, in CI or locally.
# /// script
# dependencies = ["pyyaml==6.0.2"]
# ///
from __future__ import annotations

import json
import sys
from pathlib import Path

import yaml


def main() -> None:
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    crd_dir, out_dir = Path(sys.argv[1]), Path(sys.argv[2])

    written = 0
    for f in sorted(crd_dir.glob("*.yaml")):
        for doc in yaml.safe_load_all(f.read_text()):
            if not doc or doc.get("kind") != "CustomResourceDefinition":
                continue
            group = doc["spec"]["group"]
            kind = doc["spec"]["names"]["kind"].lower()
            for version in doc["spec"]["versions"]:
                schema = version.get("schema", {}).get("openAPIV3Schema")
                if not schema:
                    continue
                out = out_dir / group / f"{kind}_{version['name']}.json"
                out.parent.mkdir(parents=True, exist_ok=True)
                out.write_text(json.dumps(schema, indent=2))
                written += 1
                print(f"wrote {out}")
    if not written:
        sys.exit(f"no CRD schemas found under {crd_dir}")


if __name__ == "__main__":
    main()
