#!/usr/bin/env bash
set -euo pipefail

input=$(cat)

if ! echo "$input" | grep -q "FAKE KUBECONFORM"; then
  echo "[]"
  exit 1
fi

echo '{
  "resources": [
    {
      "filename": "stdin",
      "kind": "Deployment",
      "name": "api",
      "namespace": "production",
      "version": "apps/v1",
      "status": "statusInvalid",
      "msg": "problem validating schema",
      "validationErrors": [
        {"path": "spec.template.spec.containers[0].resources", "msg": "missing property 'resources'"},
        {"path": "spec.template.spec.containers[0].image", "msg": "must not be empty"}
      ]
    },
    {
      "filename": "stdin",
      "kind": "Service",
      "name": "api",
      "namespace": "production",
      "version": "v1",
      "status": "statusValid",
      "msg": ""
    },
    {
      "filename": "stdin",
      "kind": "ConfigMap",
      "name": "config",
      "namespace": "default",
      "version": "v1",
      "status": "statusSkipped",
      "msg": "could not find schema"
    },
    {
      "filename": "stdin",
      "kind": "Ingress",
      "name": "api",
      "namespace": "production",
      "version": "networking.k8s.io/v1",
      "status": "statusError",
      "msg": "failed to download schema",
      "validationErrors": []
    }
  ]
}'
