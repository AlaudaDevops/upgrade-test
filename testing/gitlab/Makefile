STAGE ?= create
EXTRA_ARGS ?= "--alluredir=allure-results"

.PHONY: run
run:
	pytest -m "$(STAGE)" $(EXTRA_ARGS)

.PHONY: create
create:
	pytest -m "create or prepare or check" $(EXTRA_ARGS)

.PHONY: upgrade
upgrade:
	pytest -m "upgrade or check" $(EXTRA_ARGS)

.PHONY: cleanup
cleanup:
	pytest -m "cleanup" $(EXTRA_ARGS)

Shortcut for --capture=no
