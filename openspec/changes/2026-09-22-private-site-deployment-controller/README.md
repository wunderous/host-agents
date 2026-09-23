# Private site deployment controller

This change moves deployment authority for www.opute.io into the private
repository wunderous/opute-site-deploy. The public wunderous/host-agents
repository builds and versions the static-site image; the private controller
selects a successful image build from main, resolves its immutable digest, and
deploys it through the local Host Agent recipe.

The checklist in tasks.md is the implementation and evidence ledger. A task
stays open until its cited command or live source proves the outcome.
