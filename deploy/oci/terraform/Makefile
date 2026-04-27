PLAN_FILE := tfplan

# Targeting flags scope each phase. Network is foundational and pulled
# in by every other module, so subsequent plans implicitly include it.
NETWORK_TARGETS := -target=module.network
MANAGED_TARGETS := -target=module.network -target=module.db -target=module.streaming -target=module.cache -target=module.objectstorage
OKE_TARGETS     := -target=module.network -target=module.oke
FLEET_TARGETS   := -target=module.network -target=module.fleet

.PHONY: init plan-network apply-network plan-managed apply-managed plan-oke apply-oke plan-fleet apply-fleet plan-destroy destroy clean

init:
	terraform init

# Network — foundational, free.
plan-network: init
	terraform plan $(NETWORK_TARGETS) -out=$(PLAN_FILE)

apply-network:
	@test -f $(PLAN_FILE) || { echo "Run 'make plan-network' first."; exit 1; }
	@echo ""
	@echo "*** Review the plan above. Press Enter to apply, Ctrl-C to abort. ***"
	@read _
	terraform apply $(PLAN_FILE)
	@rm -f $(PLAN_FILE)

# Managed services — Postgres + Streaming + Cache + Object Storage.
plan-managed: init
	terraform plan $(MANAGED_TARGETS) -out=$(PLAN_FILE)

apply-managed:
	@test -f $(PLAN_FILE) || { echo "Run 'make plan-managed' first."; exit 1; }
	@echo ""
	@echo "*** Review the plan above. Press Enter to apply, Ctrl-C to abort. ***"
	@echo "*** This step provisions billable resources (~\$$0.20/hour). ***"
	@read _
	terraform apply $(PLAN_FILE)
	@rm -f $(PLAN_FILE)

# OKE + ClickHouse.
plan-oke: init
	terraform plan $(OKE_TARGETS) -out=$(PLAN_FILE)

apply-oke:
	@test -f $(PLAN_FILE) || { echo "Run 'make plan-oke' first."; exit 1; }
	@echo ""
	@echo "*** Review the plan above. OKE provisioning takes ~10 min. ***"
	@read _
	terraform apply $(PLAN_FILE)
	@rm -f $(PLAN_FILE)

# Daemon fleet.
plan-fleet: init
	terraform plan $(FLEET_TARGETS) -out=$(PLAN_FILE)

apply-fleet:
	@test -f $(PLAN_FILE) || { echo "Run 'make plan-fleet' first."; exit 1; }
	@echo ""
	@echo "*** Review the plan above. Press Enter to apply, Ctrl-C to abort. ***"
	@read _
	terraform apply $(PLAN_FILE)
	@rm -f $(PLAN_FILE)

# Full destroy. Always plans first so you see exactly what's going.
plan-destroy: init
	terraform plan -destroy -out=$(PLAN_FILE).destroy

destroy: plan-destroy
	@echo ""
	@echo "*** This will DESTROY every resource in the smoke compartment. ***"
	@echo "*** Includes: DB, bucket, cluster, VMs, networks. All data lost. ***"
	@echo "*** Press Enter to proceed, Ctrl-C to abort. ***"
	@read _
	terraform apply $(PLAN_FILE).destroy
	@rm -f $(PLAN_FILE).destroy

clean:
	rm -f $(PLAN_FILE) $(PLAN_FILE).destroy
