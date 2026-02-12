#!/usr/bin/env python3
"""
Check if local changes in bindata/ would be overwritten by sync-manifests-from-submodule.

This script:
1. Detects which files in bindata/ have local changes (git status/diff)
2. Simulates what sync-manifests-from-submodule would generate
3. Reports which modified files would be overwritten
"""

import os
import sys
import yaml
import subprocess
from pathlib import Path
from typing import Set, Dict, List, Tuple


# Custom YAML dumper (same as sync_manifests.py)
def string_representer(dumper, data):
    if str(data).lower() in ("true", "false"):
        return dumper.represent_scalar("tag:yaml.org,2002:str", data, style='"')
    if data == "":
        return dumper.represent_scalar("tag:yaml.org,2002:str", data, style='"')
    if "\n" in data:
        return dumper.represent_scalar("tag:yaml.org,2002:str", data, style="|")
    if hasattr(data, "style") and data.style == '"':
        return dumper.represent_scalar("tag:yaml.org,2002:str", data, style='"')
    return dumper.represent_scalar("tag:yaml.org,2002:str", data)


yaml.add_representer(str, string_representer)


def get_modified_bindata_files() -> Set[str]:
    """Get list of modified files in bindata/ using git."""
    try:
        # Get staged and unstaged changes
        result = subprocess.run(
            ["git", "diff", "--name-only", "HEAD"],
            capture_output=True,
            text=True,
            check=True,
        )

        modified_files = set()
        for line in result.stdout.strip().split('\n'):
            if line.startswith('bindata/'):
                modified_files.add(line)

        return modified_files
    except subprocess.CalledProcessError as e:
        print(f"Error getting git status: {e}")
        sys.exit(1)


def clean_name(name: str, kind: str) -> str:
    """Clean resource names (same logic as sync_manifests.py)."""
    if kind in ["RoleBinding", "ClusterRoleBinding", "Role", "Service", "ClusterRole"]:
        name = name.replace("kueue-", "")
    if kind in ["RoleBinding", "ClusterRoleBinding"]:
        name = name.replace("-rolebinding", "").replace("-binding", "")
    elif kind == "Role":
        name = name.replace("-role", "")
    return name


def process_manifests(src_dir: Path, bindata_dir: Path) -> Dict[str, str]:
    """
    Process manifests from make sync-manifests-from-submodule and return mapping of file paths to content.
    This simulates what sync-manifests-from-submodule would generate.
    """
    try:
        result = subprocess.run(
            ["python3", "hack/sync_manifests.py", "--src-dir", src_dir],
            capture_output=True,
            text=True,
            check=True,
        )
    except subprocess.CalledProcessError as e:
        print(f"Failed to run make sync-manifests-from-submodule: {e}")
        print(f"stderr: {e.stderr}")
        sys.exit(1)

    # Parse YAML documents
    try:
        docs = list(yaml.safe_load_all(result.stdout))
    except yaml.YAMLError as e:
        print(f"Failed to parse YAML: {e}")
        sys.exit(1)

    # Define mapping (same as sync_manifests.py)
    file_map = {
        "CustomResourceDefinition": "crd",
        "ClusterRole": "clusterrole",
        "ClusterRoleBinding": "clusterrolebinding",
        "APIService": "apiservice",
        "ValidatingWebhookConfiguration": "validatingwebhook",
        "MutatingWebhookConfiguration": "mutatingwebhook",
        "Service": "service",
        "Role": "role",
        "RoleBinding": "rolebinding",
        "ServiceAccount": "serviceaccount",
        "Deployment": "deployment",
        "Secret": "secret",
    }

    namespace_updates = ["Deployment", "Service", "ServiceAccount"]
    separated_manifests = {}
    allowed_kinds = set(file_map.keys())

    # Process documents (same logic as sync_manifests.py)
    for doc in docs:
        if not isinstance(doc, dict) or "kind" not in doc:
            continue

        kind = doc["kind"]
        if kind not in allowed_kinds:
            continue

        # Update webhook configurations
        if kind in ["ValidatingWebhookConfiguration", "MutatingWebhookConfiguration"]:
            if "webhooks" in doc:
                for webhook in doc["webhooks"]:
                    ns_selector = webhook.get("namespaceSelector")
                    if ns_selector is not None and "matchExpressions" in ns_selector:
                        for expr in ns_selector["matchExpressions"]:
                            if (expr.get("key") == "kubernetes.io/metadata.name" and
                                expr.get("operator") == "NotIn"):
                                expr["values"] = [
                                    "openshift-kueue-operator" if v == "kueue-system" else v
                                    for v in expr.get("values", [])
                                ]
                    client_config = webhook.get("clientConfig", {})
                    service_config = client_config.get("service", {})
                    if service_config.get("namespace") == "kueue-system":
                        service_config["namespace"] = "openshift-kueue-operator"

        # Update namespace
        if kind in namespace_updates and "metadata" in doc:
            doc["metadata"]["namespace"] = "openshift-kueue-operator"

        # Parametrize image in Deployment
        if kind == "Deployment" and doc["metadata"]["name"] == "kueue-controller-manager":
            for container in doc["spec"]["template"]["spec"]["containers"]:
                if container["name"] == "manager":
                    container["image"] = "${IMAGE}"
            doc["spec"]["template"]["metadata"]["labels"]["app.openshift.io/name"] = "kueue"

        base_filename = file_map[kind]
        if base_filename not in separated_manifests:
            separated_manifests[base_filename] = []
        separated_manifests[base_filename].append(doc)

    # Generate file paths and content
    file_contents = {}

    for base_filename, content in separated_manifests.items():
        for i, doc in enumerate(content):
            # Skip CRD in main directory (only in crds/)
            if base_filename == "crd":
                continue

            # Determine file path
            if base_filename in ["rolebinding", "clusterrolebinding", "role", "service", "clusterrole"]:
                name = doc["metadata"]["name"]
                name = clean_name(name, doc["kind"])
                if base_filename == "service":
                    bindata_file = bindata_dir / f"{name}.yaml"
                elif base_filename == "clusterrole":
                    bindata_file = bindata_dir / "clusterroles" / f"clusterrole-{name}.yaml"
                else:
                    bindata_file = bindata_dir / f"{base_filename}-{name}.yaml"
            else:
                bindata_file = bindata_dir / f"{base_filename}.yaml"

            # Generate content
            new_content = yaml.dump(doc, default_flow_style=False)
            if base_filename in ["clusterrole", "crd"]:
                new_content = "---\n" + new_content

            file_contents[str(bindata_file)] = new_content

    # Process clusterroles and CRDs separately
    for base_filename, content in separated_manifests.items():
        if base_filename in ["clusterrole", "crd"]:
            for i, doc in enumerate(content):
                name = doc["metadata"]["name"]
                name = clean_name(name, doc["kind"])
                if base_filename == "clusterrole":
                    bindata_file = bindata_dir / "clusterroles" / f"clusterrole-{name}.yaml"
                else:
                    bindata_file = bindata_dir / "crds" / f"crd-{name}.yaml"

                new_content = "---\n" + yaml.dump(doc, default_flow_style=False)
                file_contents[str(bindata_file)] = new_content

    return file_contents


def check_conflicts(modified_files: Set[str], generated_files: Dict[str, str]) -> List[Tuple[str, bool]]:
    """
    Check which modified files would be overwritten.
    Returns list of (filename, content_differs) tuples.
    """
    conflicts = []

    for filepath in modified_files:
        if filepath in generated_files:
            # Read current content
            if os.path.exists(filepath):
                with open(filepath, 'r') as f:
                    current_content = f.read()

                # Compare with what would be generated
                new_content = generated_files[filepath]
                differs = current_content != new_content
                conflicts.append((filepath, differs))

    return conflicts


def main():
    # Configuration
    src_dir = Path("upstream/kueue/src/config/default/")
    bindata_dir = Path("bindata/assets/kueue-operator")

    print("Checking for conflicts between local changes and sync-manifests-from-submodule...\n")

    # Check if source directory exists
    if not src_dir.exists():
        print(f"Error: Source directory {src_dir} does not exist.")
        print("Make sure the upstream submodule is initialized.")
        sys.exit(1)

    # Get modified files
    print("Step 1: Finding modified files in bindata/...")
    modified_files = get_modified_bindata_files()

    if not modified_files:
        print("✓ No modified files in bindata/")
        return

    print(f"Found {len(modified_files)} modified file(s):")
    for f in sorted(modified_files):
        print(f"  - {f}")
    print()

    # Generate what sync would create
    print("Step 2: Simulating sync-manifests-from-submodule...")
    generated_files = process_manifests(src_dir, bindata_dir)
    print(f"Would generate {len(generated_files)} file(s)\n")

    # Check for conflicts
    print("Step 3: Checking for conflicts...")
    conflicts = check_conflicts(modified_files, generated_files)

    if not conflicts:
        print("✓ No conflicts found!")
        print("  Modified files would not be affected by sync-manifests-from-submodule")
        return

    # Report conflicts
    print(f"\n⚠️  Found {len(conflicts)} file(s) that would be affected:\n")

    files_with_diff = []
    files_same_content = []

    for filepath, differs in conflicts:
        if differs:
            files_with_diff.append(filepath)
        else:
            files_same_content.append(filepath)

    if files_with_diff:
        print(f"Files with DIFFERENT content ({len(files_with_diff)}):")
        print("  (Your changes would be OVERWRITTEN)")
        for f in sorted(files_with_diff):
            print(f"  ✗ {f}")
        print()

    if files_same_content:
        print(f"Files with SAME content ({len(files_same_content)}):")
        print("  (No actual conflict, changes already match)")
        for f in sorted(files_same_content):
            print(f"  ✓ {f}")
        print()

    # Summary
    if files_with_diff:
        print("RECOMMENDATION:")
        print("  Your local changes in the files marked with ✗ would be overwritten.")
        print("  You should either:")
        print("    1. Commit or stash your changes before running sync-manifests-from-submodule")
        print("    2. Review the differences and decide which changes to keep")
        sys.exit(1)
    else:
        print("All modified files have the same content as what would be generated.")
        print("Safe to run sync-manifests-from-submodule.")


if __name__ == "__main__":
    main()
