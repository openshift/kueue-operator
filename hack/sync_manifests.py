import os
import yaml
import requests
import sys

# Custom YAML dumper to preserve multi-line strings in block style, respect double quotes, and handle boolean-like strings.
def string_representer(dumper, data):
    # Check if the string is a boolean-like string ("true" or "false")
    if str(data).lower() in ("true", "false"):
        return dumper.represent_scalar("tag:yaml.org,2002:str", data, style='"')
    # Check if the string is empty
    if data == "":
        return dumper.represent_scalar("tag:yaml.org,2002:str", data, style='"')
    # Check if the string contains newlines
    if "\n" in data:
        return dumper.represent_scalar("tag:yaml.org,2002:str", data, style="|")
    # Check if the string was originally quoted with double quotes
    if hasattr(data, "style") and data.style == '"':
        return dumper.represent_scalar("tag:yaml.org,2002:str", data, style='"')
    return dumper.represent_scalar("tag:yaml.org,2002:str", data)

yaml.add_representer(str, string_representer)

# Get the latest version of Kueue.
def fetch_latest_version():
    api_url = "https://api.github.com/repos/kubernetes-sigs/kueue/releases/latest"
    try:
        response = requests.get(api_url)
        response.raise_for_status()
        latest_version = response.json().get("tag_name", "").lstrip("v")
        if not latest_version:
            raise ValueError("Failed to fetch the latest version")
        return latest_version
    except requests.RequestException as e:
        print(f"Failed to fetch the latest version: {e}")
        exit(1)


if len(sys.argv) > 1:
    version = sys.argv[1]
else:
    version = fetch_latest_version()

print(f"Using Kueue version: {version}")

# Define directories
bindata_dir = "bindata/assets/kueue-operator"
manifests_url = f"https://github.com/kubernetes-sigs/kueue/releases/download/v{version}/manifests.yaml"
input_file = f"manifests.yaml"

# Download manifests.yaml.
print(f"Downloading manifests.yaml from {manifests_url} ...")
response = requests.get(manifests_url)
if response.status_code != 200:
    raise RuntimeError(f"Failed to download manifests.yaml (HTTP {response.status_code})")

with open(input_file, "wb") as f:
    f.write(response.content)

# Read and split YAML file.
try:
    with open(input_file, "r") as f:
        docs = list(yaml.safe_load_all(f))
except yaml.YAMLError as e:
    print(f"Failed to parse YAML file: {e}")
    exit(1)

# Define a mapping of 'kind' to filenames.
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

# Resources that need namespace updates (excluding Secret)
namespace_updates = ["Deployment", "Service", "ServiceAccount"]

# Clean up names for RoleBinding, ClusterRoleBinding, Role, Service, and ClusterRole
def clean_name(name, kind):
    # Remove 'kueue-' prefix from the name
    if kind in ["RoleBinding", "ClusterRoleBinding", "Role", "Service", "ClusterRole"]:
        name = name.replace("kueue-", "")
    # Remove suffixes for specific kinds
    if kind in ["RoleBinding", "ClusterRoleBinding"]:
        name = name.replace("-rolebinding", "").replace("-binding", "")
    elif kind == "Role":
        name = name.replace("-role", "")
    return name

# Organize manifests for output
separated_manifests = {}
allowed_kinds = set(file_map.keys())  # Process only these kinds

for doc in docs:
    if not isinstance(doc, dict) or "kind" not in doc:
        continue  # Skip invalid or empty YAML docs

    kind = doc["kind"]
    if kind not in allowed_kinds:
        continue  # Skip unrelated kinds

    # For Validating and Mutating webhook configurations, update namespace selectors and clientConfig.
    if kind in ["ValidatingWebhookConfiguration", "MutatingWebhookConfiguration"]:
        if "webhooks" in doc:
            for webhook in doc["webhooks"]:
                # Update namespaceSelector match expressions.
                ns_selector = webhook.get("namespaceSelector")
                if ns_selector is not None and "matchExpressions" in ns_selector:
                    for expr in ns_selector["matchExpressions"]:
                        if expr.get("key") == "kubernetes.io/metadata.name" and expr.get("operator") == "NotIn":
                            expr["values"] = [
                                "openshift-kueue-operator" if v == "kueue-system" else v 
                                for v in expr.get("values", [])
                            ]
                # Update clientConfig service namespace.
                client_config = webhook.get("clientConfig", {})
                service_config = client_config.get("service", {})
                if service_config.get("namespace") == "kueue-system":
                    service_config["namespace"] = "openshift-kueue-operator"
    
    # Update namespace for specific resources (excluding Secret).
    if kind in namespace_updates and "metadata" in doc:
        doc["metadata"]["namespace"] = "openshift-kueue-operator"

    # Parametrize the image field in Deployment.
    if kind == "Deployment" and doc["metadata"]["name"] == "kueue-controller-manager":
        for container in doc["spec"]["template"]["spec"]["containers"]:
            if container["name"] == "manager":
                container["image"] = "${IMAGE}"

    # Add label for network policy for deployment
    if kind == "Deployment" and doc["metadata"]["name"] == "kueue-controller-manager":
        doc["spec"]["template"]["metadata"]["labels"]["app.openshift.io/name"] = "kueue"

    # Store files in `bindata/assets/kueue-operator/`
    base_filename = file_map[kind]
    if base_filename not in separated_manifests:
        separated_manifests[base_filename] = []

    separated_manifests[base_filename].append(doc)

def write_yaml_if_changed(bindata_file, doc, add_header=False):
    # Read existing file content if it exists.
    existing_content = ""
    if os.path.exists(bindata_file):
        with open(bindata_file, "r") as f:
            existing_content = f.read()

    # Generate new content.
    new_content = yaml.dump(doc, default_flow_style=False)
    if add_header:
        new_content = "---\n" + new_content

    # Write the new content only if it has changed.
    if new_content != existing_content:
        print(f"Writing {bindata_file}...")
        with open(bindata_file, "w") as f:
            f.write(new_content)
    else:
        print(f"Skipping {bindata_file} as content has not changed.")

# Write YAML files to bindata directory.
for base_filename, content in separated_manifests.items():
    for i, doc in enumerate(content):
        # Skip creating crd.yaml in bindata/assets/kueue-operator/
        if base_filename == "crd":
            continue

        # Handle RoleBinding, ClusterRoleBinding, Role, Service, and ClusterRole naming.
        if base_filename in ["rolebinding", "clusterrolebinding", "role", "service", "clusterrole"]:
            name = doc["metadata"]["name"]
            name = clean_name(name, doc["kind"])
            if base_filename == "service":
                # Remove 'service-' prefix for Service resources.
                bindata_file = os.path.join(bindata_dir, f"{name}.yaml")
            elif base_filename == "clusterrole":
                # Write ClusterRole files to the clusterrole directory.
                bindata_file = os.path.join(bindata_dir, "clusterroles", f"clusterrole-{name}.yaml")
            else:
                bindata_file = os.path.join(bindata_dir, f"{base_filename}-{name}.yaml")
        else:
            bindata_file = os.path.join(bindata_dir, f"{base_filename}.yaml")

        write_yaml_if_changed(bindata_file, doc, add_header=base_filename in ["clusterrole", "crd"])

# Break ClusterRole and CRD into separate files.
for base_filename, content in separated_manifests.items():
    if base_filename in ["clusterrole", "crd"]:
        for i, doc in enumerate(content):
            name = doc["metadata"]["name"]
            name = clean_name(name, doc["kind"])
            if base_filename == "clusterrole":
                bindata_file = os.path.join(bindata_dir, "clusterroles", f"clusterrole-{name}.yaml")
            else:
                bindata_file = os.path.join(bindata_dir, "crds", f"crd-{name}.yaml")

            write_yaml_if_changed(bindata_file, doc, add_header=True)

# Delete manifests.yaml after processing.
os.remove(input_file)
print(f"Processing complete. YAML manifests saved in {bindata_dir}/")
