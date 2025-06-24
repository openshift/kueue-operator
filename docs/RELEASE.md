# Releasing the Operator

This document walks through the release process.

## Konflux

Konflux is used for releasing our images. We have two applications for our operator: kueue and kueue-operator.

To release, it is necessary to understand ReleasePlanAdmissions (RPA), EnterpriseContractPolicy (ECP), ReleasePlans (RP), Releases and Snapshots.
You can find these definitions in konflux-release-data in our internal gitlab.

A VPN is required for these links but you can find the definitions [here](https://gitlab.cee.redhat.com/releng/konflux-release-data).

### EnterpriseContractPolicy

The ECP is a policy that verifies application defined policities and tests if the images pass these policies.
If the enterprise contract fails then the release will be blocked.

We have enabled these policies on PRs so if the CI starts failing due to enterprise contract policies then you can expect the release to fail.

### ReleasePlanAdmissions

ReleasePlanAdmissions control the tagging of the images and where they are promoted when released.
These objects also control what ECP runs on a release.
Each release of Kueue will need a new RPA so that one can change the tagging of our images.

For operand, we tag our images with the upstream tag. For 1.0, the operand images are tagged with 0.11.

For the operator, we tag our images with our operator release. So for 1.0, they are tagged with 1.0.

### ReleasePlans

When you kick off a release, you point to the RP. These will reference a RPA.
The RP defines some metadata that doesn't change from release. 

### Release

This is a manual CR that triggers the release. 
You must specify the snapshot of the intended product you want to release.
Be careful here as any mishap leads to promotion of an image to production.

```yaml
apiVersion: appstudio.redhat.com/v1alpha1
kind: Release
metadata:
  name: RELEASE_NAME
  namespace: kueue-operator-tenant
  labels:
    release.appstudio.openshift.io/author: AUTHOR_NAME
spec:
  releasePlan: RELEASE_PLAN
  snapshot: SNAPSHOT
  data:
    releaseNotes:
    ...
```

One adds release notes to this YAML based on the release.

## Branching

The operator is platform agnostic. The operator/operand are built from a release branch.

For 1.0, it is based on Kueue 0.11.

Kueue is based on the release branch and 0.11 so the application is kueue-0-11.

The operator is built from release-1 so the application is kueue-operator-1-0.

## Release Workflow

Hopefully, some day this can be automated.

To release Red Hat Build of Kueue:

1. Release the Kueue Operand.
2. Update the operator bundle to use release operand images.
3. Release the Kueue Operator.
4. Update the fbc to use the released bundle image.
5. Release the FBC for each Openshift version.

## Kueue Operand

For our GA release, we promote our images to registry.redhat.io/kueue/kueue-rhel9.

Below is an example Release you could use to promote Kueue images.
You must pick a snapshot and then you run this on the konflux cluster.

## Kueue Operator

Kueue operator corresponds to 3 components: operator, bundle and must-gather.

All of these are promoted together when triggering a release.

| Component | GA Repository |
| --------- | -------------- |
| Kueue Operator | registry.redhat.io/kueue/kueue-rhel9-operator |
| Kueue Bundle   | registry.redhat.io/kueue/kueue-operator-bundle |
| Kueue Must Gather | registry.redhat.io/kueue/kueue-must-gather-rhel9 |

All of these components are bundled together in a snapshot so you only need to need to find
the relevant snapshot for your release.

### Bundle Updates

A z stream or a new release requires an update of the VERSION in the Makefile and the generate-bundle script.

Nudges keep the bundle images up to date but if you have to generate the images,
you should set the environment variables OPERATOR_IMAGE, KUEUE_IMAGE and KUEUE_MUST_GATHER_IMAGE to the values set in the 
manifest file.
This way you won't overrite the images.

## FBC Release

FBC is defined as a separate application per openshift release. 
Each OCP release has its own catalog.
Our naming structure is kueue-fbc-OCP-VERSION.

Once the operator/operand are released, one needs to update the FBC to use the released bundle.

This is done in the kueue-fbc [repo](https://github.com/openshift/kueue-fbc).

One updates the [catalog-template](https://github.com/openshift/kueue-fbc/pull/16/files) and runs generate-fbc.sh.

Once the fbc merges, you must create a Release for the FBC.

Each catalog requires its own release. 
You pick the snapshot for your targed fbc and then you target the release plan to your OCP version.
