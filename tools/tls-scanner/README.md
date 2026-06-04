# Running TLS-Scanner

## Step 1 

Clone tls-scanner (https://github.com/openshift/tls-scanner).

And cd into that directory.

## Step 2

It is best to build and publish images locally.

I already built and published an image that you could use.

Feel free to use: quay.io/kevin-oss/tls-scanner

## Step 3

Set environment variables to lower CPU requirements and to run for openshift-kueue-operator
export SCANNER_IMAGE=quay.io/kevin-oss/tls-scanner
export SCANNER_CPU=1
export NAMESPACE_FILTER=openshift-kueue-operator

Run ./deploy.sh deploy

## Step 4

The scan on openshift-kueue-operator will be dumped to a file called artifacts.

