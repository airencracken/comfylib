#!/usr/bin/env python3
"""Synchronize release-independent suite helpers and browser assets into apps."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

SOURCE = Path(__file__).resolve().parents[1]


def app_id(root):
    lines=(root / "go.mod").read_text().splitlines()
    modules=[line.split()[1] for line in lines if line.startswith("module ")]
    if len(modules)!=1:
        raise ValueError("destination needs an application go.mod")
    app=modules[0].rsplit("/",1)[-1]
    if app not in ("imvault", "witmoot", "songstead", "folkweave", "tidings"):
        raise ValueError("destination is not a Comfyware application module")
    return app


def files(root):
    app = app_id(root)
    if app not in ("imvault", "witmoot", "songstead", "folkweave", "tidings"):
        raise ValueError("destination must be a Comfyware application checkout")
    web = {"imvault": "web", "songstead": "web", "witmoot": "forum", "folkweave": "folkweave", "tidings": "reader"}[app]
    static = Path("internal") / web / "static"
    css = static / ("css/comfy-components.css" if app == "imvault" else "comfy-components.css")
    js = static / ("js/comfy-suite.js" if app == "imvault" else "comfy-suite.js")
    result = [(SOURCE / "ui/components.css", css), (SOURCE / "ui/suite.js", js)]
    result.extend((p, Path("internal/suite") / p.name) for p in sorted((SOURCE / "suite").glob("*.go")))
    return result


def sync(root, check=False):
    app=app_id(root)
    manifest = {}
    for source, relative in files(root):
        data = source.read_bytes()
        if source.suffix == ".go":
            data = subprocess.run(["gofmt"], input=data, capture_output=True, check=True).stdout
        destination = root / relative
        manifest[str(relative)] = hashlib.sha256(data).hexdigest()
        if check:
            if not destination.is_file() or destination.read_bytes() != data:
                raise ValueError(f"suite source differs: {destination}")
        else:
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(data)
    for name in ("verify_suite.py", "scan_artifacts.py"):
        destination=root / "scripts" / name
        data=(SOURCE / "tools" / name).read_bytes()
        if check:
            if not destination.is_file() or destination.read_bytes()!=data:
                raise ValueError(f"suite tool differs: {destination}")
        else:
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(data)
    destination=root / "scripts/browser-controls.mjs"
    controls=(SOURCE / "ui/uitest/controls.mjs").read_bytes()
    if check:
        if not destination.is_file() or destination.read_bytes()!=controls:
            raise ValueError(f"browser suite helper differs: {destination}")
    else:
        destination.write_bytes(controls)
    module = {"imvault": "imvault", "witmoot": "witmoot"}.get(app, "github.com/airencracken/" + app)
    adapter = (SOURCE / "tools/oidc_provider.go.txt").read_text().replace("APP_MODULE", module).encode()
    adapter = subprocess.run(["gofmt"], input=adapter, capture_output=True, check=True).stdout
    destination = root / "internal/sharedoidc/provider.go"
    manifest["internal/sharedoidc/provider.go"] = hashlib.sha256(adapter).hexdigest()
    if check:
        if not destination.is_file() or destination.read_bytes() != adapter:
            raise ValueError(f"OIDC adapter differs: {destination}")
    else:
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(adapter)
    adapter_test = (SOURCE / "tools/oidc_provider_test.go.txt").read_text().replace("APP_MODULE", module).encode()
    adapter_test = subprocess.run(["gofmt"], input=adapter_test, capture_output=True, check=True).stdout
    test_path = root / "internal/sharedoidc/provider_test.go"
    manifest["internal/sharedoidc/provider_test.go"] = hashlib.sha256(adapter_test).hexdigest()
    if check:
        if not test_path.is_file() or test_path.read_bytes() != adapter_test:
            raise ValueError(f"OIDC tests differ: {test_path}")
    else:
        test_path.write_bytes(adapter_test)
    integration = (SOURCE / "tools/shared_auth_test.go.txt").read_text().replace("APP_MODULE", module).encode()
    integration = subprocess.run(["gofmt"], input=integration, capture_output=True, check=True).stdout
    integration_path = root / "internal/sharedoidc/auth_test.go"
    manifest["internal/sharedoidc/auth_test.go"] = hashlib.sha256(integration).hexdigest()
    if check:
        if not integration_path.is_file() or integration_path.read_bytes() != integration:
            raise ValueError(f"shared authentication tests differ: {integration_path}")
    else:
        integration_path.write_bytes(integration)
    path = root / "scripts/suite-assets.json"
    expected = {"source": "github.com/airencracken/comfylib", "files": manifest}
    if check:
        if json.loads(path.read_text()) != expected:
            raise ValueError(f"suite manifest differs: {path}")
    else:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(expected, indent=2) + "\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("apps", type=Path, nargs="+")
    args = parser.parse_args()
    for root in args.apps:
        sync(root.resolve(), args.check)
