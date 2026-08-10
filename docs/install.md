# Prerequisites

Docker, [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation),
kubectl, [Helm](https://helm.sh/docs/intro/install/) 3+, and
[mkcert](https://github.com/FiloSottile/mkcert) for the local TLS certificate.

```sh
brew install kind kubectl helm mkcert nss
mkcert -install     # adds the local CA to the system keychain
```

`mkcert -install` asks for your password. Without it the browser shows a
certificate warning — the CA has to be trusted by the machine, and nothing
inside the cluster can do that.
