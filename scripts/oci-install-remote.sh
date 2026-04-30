#!/bin/bash
# Runs ON the cp-vm via `sudo bash -s`. The Makefile pipes this script
# over ssh after staging artifacts in /tmp/. Atomic install + reload + restart.
set -euo pipefail

install -m 0755 -o root -g root        /tmp/okesu-cp.new        /usr/local/bin/okesu-cp
install -m 0640 -o root -g okesu-cp    /tmp/cp.yaml.new         /etc/okesu-cp/cp.yaml
install -m 0640 -o root -g okesu-cp    /tmp/okesu-cp.env.new    /etc/default/okesu-cp
install -m 0644 -o root -g root        /tmp/okesu-cp.service.new /etc/systemd/system/okesu-cp.service

rm -rf /etc/okesu-cp/secrets
mv /tmp/secrets /etc/okesu-cp/secrets
chown -R okesu-cp:okesu-cp /etc/okesu-cp/secrets
chmod -R go-rwx /etc/okesu-cp/secrets

systemctl daemon-reload
systemctl enable --now okesu-cp
systemctl restart okesu-cp
