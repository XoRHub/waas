#!/usr/bin/env python3
"""Generate the GitLab child pipeline from images.yaml + discovered manifests.

Discovery: every {base,desktop,apps}/*/manifest.yaml becomes, per
variant, one NATIVE build job per architecture (runner tags amd/arm —
no QEMU) plus one merge job assembling the arch tags into the manifest
list. Jobs are placed in stages by dependency depth (a variant whose
`from` names another variant builds one stage later), and each arch
chain is independent: `ARG BASE_IMAGE` points at the parent's SAME-ARCH
tag pushed earlier in the same pipeline.

Output: build-pipeline.yml (consumed by the parent's trigger job).
"""
from __future__ import annotations

import os
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent
LAYER_DIRS = ("base", "desktop", "apps")
# GitLab runner tag per build platform. An arch without a native runner
# fleet must not appear in a manifest.
RUNNER_TAGS = {"linux/amd64": "amd", "linux/arm64": "arm"}


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


def validate_archs(variants: dict[str, dict]) -> None:
    """Every arch must have a runner mapping; a child cannot build an
    arch its parent does not publish (BASE_IMAGE is per-arch)."""
    for name, v in variants.items():
        for arch in v["archs"]:
            if arch not in RUNNER_TAGS:
                sys.exit(f"{name}: no runner tag mapped for arch {arch!r}")
        if v["from"]:
            missing = set(v["archs"]) - set(variants[v["from"]]["archs"])
            if missing:
                sys.exit(f"{name}: parent {v['from']!r} does not build {sorted(missing)}")


def emit(variants: dict[str, dict], cfg: dict, strategy: str) -> str:
    depths = {name: stage_of(variants, name) for name in variants}
    stages = [f"layer-{d}" for d in sorted(set(depths.values()))]
    scan = cfg.get("scan", {})

    pipeline: dict = {
        "stages": stages,
        # All heavy lifting lives in ci/*.sh so this generated YAML
        # stays a thin dispatch layer.
        "default": {
            "image": "docker:28.0",
            "services": [{"name": "docker:28.0-dind", "command": ["--tls=false"]}],
            "interruptible": True,
            # merge jobs (registry-only work) run on the amd fleet;
            # build jobs carry their own arch tag below.
            "tags": ["amd"],
        },
        "variables": {
            # Kubernetes executor: build/service containers share one pod
            # network namespace, so dind is reachable at localhost, not
            # the service alias; TLS disabled to avoid the /certs sharing
            # race some runners hit on first cert generation.
            "DOCKER_HOST": "tcp://localhost:2375",
            "DOCKER_TLS_CERTDIR": "",
            "TRIVY_SEVERITY": scan.get("severity", "HIGH,CRITICAL"),
            "TRIVY_IGNORE_UNFIXED": str(scan.get("ignoreUnfixed", True)).lower(),
        },
    }

    for name, v in sorted(variants.items(), key=lambda kv: (depths[kv[0]], kv[0])):
        common_vars = {
            "IMG_NAME": name,
            "IMG_CONTEXT": v["context"],
            "IMG_VERSION": v["version"],
            "IMG_BUILD_ARGS": " ".join(
                f"{k}={val}" for k, val in sorted(v["build_args"].items())
            ),
            # Parent ref minus the -g<sha>-<arch> suffix, which only the
            # build job knows (build_image.sh appends it).
            "IMG_FROM_REF": (
                f"{v['from']}:{variants[v['from']]['version']}" if v["from"] else ""
            ),
            "SMOKE_VNC": "1" if v["smoke"].get("vnc") else "0",
            "SMOKE_RDP": "1" if v["smoke"].get("rdp") else "0",
            "SMOKE_SSH": "1" if v["smoke"].get("ssh") else "0",
            "SMOKE_AUDIO": "1" if v["smoke"].get("audio") else "0",
            "SMOKE_ENV": " ".join(
                f"{k}={val}" for k, val in sorted(v["smoke"].get("env", {}).items())
            ),
        }

        for arch in v["archs"]:
            suffix = arch.removeprefix("linux/")
            job = {
                "stage": f"layer-{depths[name]}",
                # qemu strategy: everything on the amd fleet, the build
                # script installs binfmt when the target arch differs.
                "tags": ["amd" if strategy == "qemu" else RUNNER_TAGS[arch]],
                "variables": {**common_vars, "IMG_ARCH": arch},
                # Grandchild jobs run from the repo root (monorepo): the
                # build script and its manifests live under waas-images/.
                "script": ["cd waas-images", "sh ci/build_image.sh"],
            }
            if v["from"]:
                job["needs"] = [f"build:{v['from']}:{suffix}"]
            pipeline[f"build:{name}:{suffix}"] = job

        pipeline[f"merge:{name}"] = {
            "stage": f"layer-{depths[name]}",
            "needs": [f"build:{name}:{a.removeprefix('linux/')}" for a in v["archs"]],
            "variables": {
                "IMG_NAME": name,
                "IMG_VERSION": v["version"],
                "IMG_ARCHS": ",".join(v["archs"]),
            },
            "script": ["cd waas-images", "sh ci/merge_image.sh"],
        }

    return yaml.safe_dump(pipeline, sort_keys=False, width=120)


def main() -> None:
    # Operational fallback: set the CI variable WAAS_IMAGES_BUILD_STRATEGY
    # to "qemu" (e.g. arm fleet down) to route every build job to the amd
    # fleet under emulation. Same jobs, same gates, just slower.
    strategy = os.environ.get("WAAS_IMAGES_BUILD_STRATEGY", "native")
    if strategy not in ("native", "qemu"):
        sys.exit(f"WAAS_IMAGES_BUILD_STRATEGY must be native|qemu, got {strategy!r}")
    cfg = yaml.safe_load((ROOT / "images.yaml").read_text())
    variants = flatten_variants(load_manifests(), cfg)
    validate_archs(variants)
    out = ROOT / "build-pipeline.yml"
    out.write_text(emit(variants, cfg, strategy))
    print(f"generated {out.name} with {len(variants)} image(s), strategy={strategy}")


if __name__ == "__main__":
    main()
