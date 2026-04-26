# test/ — local end-to-end test fixtures

Disposable Docker images and helper scripts used to verify Control Plane and
daemon flows that need a remote target. Nothing in here ships in a production
build.

## Fixtures

| Path | Purpose | Used by |
|---|---|---|
| [`sshtarget/`](sshtarget/) | SSH server with mocked systemctl | Phase 5 deploy flow |

Each fixture has its own `README.md` with run instructions.

## Running the full Phase 5 deploy test

```bash
# 1. Build daemon + CP binaries
go build -o ./okesu     ./cmd/okesu
go build -o ./okesu-cp  ./cmd/cp

# 2. Build CP UI
( cd web && npm install && npm run build )

# 3. Build the SSH target
./test/sshtarget/build.sh
docker run -d --name okesu-sshtarget-test -p 18022:22 okesu-sshtarget:test

# 4. Start the CP pointing at the local binary + sample agents
./okesu-cp serve \
  --db ./cp.db \
  --admin-password test123 \
  --webhook-secret shared1 \
  --listen :8443 --mgmt-listen :8444 \
  --daemon-binary $(pwd)/okesu \
  --agent-files-dir $(pwd)/examples/agents

# 5. In the UI:
#    - log in as admin@local / test123
#    - Nodes → Add Node:  hostname=localhost, ssh_port=18022, ssh_user=root
#    - Deploy:  paste contents of test/sshtarget/keys/id_ed25519
#    - watch the live deploy log; expect status=succeeded

# 6. Verify on the target
ssh -i test/sshtarget/keys/id_ed25519 -p 18022 root@localhost \
  'ls -la /usr/local/bin/okesu /etc/okesu/agents/ /etc/okesu/*-mgmt-certs/'
```

## Cleanup

```bash
docker stop okesu-sshtarget-test 2>/dev/null
docker rm   okesu-sshtarget-test 2>/dev/null
rm -f cp.db cp.db-* server.crt server.key ca.crt ca.key mgmt-server.crt mgmt-server.key
```
