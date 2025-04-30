#!/bin/sh

cd fbc

for OCP_VERSION in *; do
    opm alpha render-template semver $OCP_VERSION/catalog-template.yaml --migrate-level=bundle-object-to-csv-metadata > $OCP_VERSION/catalog/kueue-operator/catalog.json;
done