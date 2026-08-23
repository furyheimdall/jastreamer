.PHONY: verify server-verify control-verify renderer-verify

verify:
	node tooling/verify-boundaries.mjs

server-verify:
	$(MAKE) -C apps/server verify

control-verify:
	$(MAKE) -C apps/control verify

renderer-verify:
	$(MAKE) -C apps/renderer verify
