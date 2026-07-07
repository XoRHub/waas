#!/usr/bin/env python3
"""Generate the GitLab child pipeline from images.yaml + discovered manifests.

Discovery: every {base,desktop,apps}/*/manifest.yaml becomes one or more
build jobs (one per variant). Jobs are placed in stages by dependency
depth (a variant whose `from` names another variant builds one stage
later), so `ARG BASE_IMAGE` always points at an image pushed earlier in
the same pipeline.

Output: build-pipeline.yml (consumed by the parent's trigger job).
"""
from __future__ import annotations

import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent
LAYER_DIRS = ("base", "desktop", "apps")


def load_manifests() -> list[dict]:
    manifests = []
    for layer in LAYER_DIRS:
        for mf in sorted(ROOT.glob(f"{layer}/*/manifest.yaml")):
            m = yaml.safe_load(mf.read_text())
            m["context"] = str(mf.parent.relative_to(ROOT))
            manifests.append(m)
    if not manifests:
        sys.exit("no manifests found — nothing to build")
    return manifests


def flatten_variants(manifests: list[dict], cfg: dict) -> dict[str, dict]:
    """One entry per publishable image, keyed by variant name."""
    defaults = cfg.get("defaults", {})
    variants: dict[str, dict] = {}
    for m in manifests:
        os_key = m.get("os", defaults.get("os"))
        os_args = cfg.get("os", {}).get(os_key, {}).get("buildArgs", {})
        for v in m.get("variants", []):
            name = v["name"]
            if name in variants:
                sys.exit(f"duplicate variant name {name!r}")
            build_args = {
                **defaults.get("buildArgs", {}),
                **os_args,
                **m.get("buildArgs", {}),
                **v.get("buildArgs", {}),
            }
            variants[name] = {
                "name": name,
                "context": m["context"],
                "version": str(m["version"]),
                "from": v.get("from", m.get("from")),
                "archs": v.get("archs", m.get("archs", defaults.get("archs", []))),
                "build_args": build_args,
                "smoke": v.get("smoke", {}),
            }
    return variants


def stage_of(variants: dict[str, dict], name: str) -> int:
    """Dependency depth: 0 for roots, parent+1 otherwise."""
    v = variants[name]
    parent = v.get("from")
    if not parent:
        return 0
    if parent not in variants:
        sys.exit(f"{name}: unknown parent image {parent!r}")
    return stage_of(variants, parent) + 1


def emit(variants: dict[str, dict], cfg: dict) -> str:
    depths = {name: stage_of(variants, name) for name in variants}
    stages = [f"layer-{d}" for d in sorted(set(depths.values()))]
    scan = cfg.get("scan", {})

    pipeline: dict = {
        "stages": stages,
        # All heavy lifting lives in ci/build_image.sh so this generated
        # YAML stays a thin dispatch layer.
        "default": {
            "image": "docker:28.0",
            "services": ["docker:28.0-dind"],
            "interruptible": True,
            # Multi-arch via QEMU on the amd64 fleet (runner tag amd).
            "tags": ["amd"],
        },
        "variables": {
            "DOCKER_TLS_CERTDIR": "/certs",
            "TRIVY_SEVERITY": scan.get("severity", "HIGH,CRITICAL"),
            "TRIVY_IGNORE_UNFIXED": str(scan.get("ignoreUnfixed", True)).lower(),
        },
    }

    for name, v in sorted(variants.items(), key=lambda kv: (depths[kv[0]], kv[0])):
        job = {
            "stage": f"layer-{depths[name]}",
            "variables": {
                "IMG_NAME": name,
                "IMG_CONTEXT": v["context"],
                "IMG_VERSION": v["version"],
                "IMG_ARCHS": ",".join(v["archs"]),
                "IMG_BUILD_ARGS": " ".join(
                    f"{k}={val}" for k, val in sorted(v["build_args"].items())
                ),
                # Parent ref minus the -g<sha> suffix, which only the
                # build job knows (build_image.sh appends it).
                "IMG_FROM_REF": (
                    f"{v['from']}:{variants[v['from']]['version']}" if v["from"] else ""
                ),
                "SMOKE_VNC": "1" if v["smoke"].get("vnc") else "0",
                "SMOKE_RDP": "1" if v["smoke"].get("rdp") else "0",
                "SMOKE_SSH": "1" if v["smoke"].get("ssh") else "0",
                "SMOKE_ENV": " ".join(
                    f"{k}={val}" for k, val in sorted(v["smoke"].get("env", {}).items())
                ),
            },
            # Grandchild jobs run from the repo root (monorepo): the
            # build script and its manifests live under waas-images/.
            "script": ["cd waas-images", "sh ci/build_image.sh"],
        }
        if v["from"]:
            job["needs"] = [f"build:{v['from']}"]
        pipeline[f"build:{name}"] = job

    return yaml.safe_dump(pipeline, sort_keys=False, width=120)


def main() -> None:
    cfg = yaml.safe_load((ROOT / "images.yaml").read_text())
    variants = flatten_variants(load_manifests(), cfg)
    out = ROOT / "build-pipeline.yml"
    out.write_text(emit(variants, cfg))
    print(f"generated {out.name} with {len(variants)} image job(s)")


if __name__ == "__main__":
    main()
