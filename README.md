# External DNS ConfigMap Provider
Provides a webhook to save [external-dns](https://github.com/kubernetes-sigs/external-dns) records to a Kubernetes ConfigMap in a form supported by [CoreDNS](https://coredns.io/). 

---

This provider was written in order to enable a split-horizon DNS configuration within a Kubernetes cluster.

The intended configuration uses external-dns to enumerate Ingresses and map them to the *internal* ClusterIP of your ingress controller.

These records are then served by CoreDNS to k8s pods, allowing them to avoid traversing the WAN when accessing cluster services (with related egress/ingress fees, WAF concerns, etc).

---

### Usage

This provider currently only supports a limited number of features (notably only A records and a single IP per host), so make sure it fits your usecase first.

The provider is intended to be deployed as a sidecar to external-dns, using the following arguments to external-dns: `--registry=noop --provider=webhook --webhook-provider-url=http://localhost:8080`

It is strongly recommended that you use Kubernetes' RBAC to limit the provider's access to only the required ConfigMap resource.