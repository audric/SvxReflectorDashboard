# FAQ

## Is a satellite forced to follow the parent's prefix for its node TGs?

**No.** Nodes on a satellite can select and monitor any TG they like. The parent's prefix only governs *routing*: a TG whose number falls under the parent's prefix is eligible to be carried out over the parent's trunk links to the wider mesh. TGs outside that range stay local to the parent + its satellites.

Cross-satellite communication still works either way — any TG keyed on one satellite is relayed to every other satellite of the same parent, so nodes on different satellites can always talk to each other regardless of TG number.

**Big caveat:** the satellite admin can apply a TG filter on their end, which narrows the set of TGs carried in and out of that satellite. If a satellite seems to be missing traffic, check its filter first — your mileage will vary based on how the owner has scoped it.

## How do I set the timezone?

**You don't need to.** The dashboard has no timezone setting by design:

- **Server and containers run in UTC.** All stored data (events, statistics) and all logs are in UTC. Containers stay in UTC regardless of the host's timezone.
- **Web pages show local time.** Timestamps are converted in the browser to the viewer's own timezone, so every visitor sees their local time.
- **The host timezone is up to you.** Setting it (e.g. `timedatectl set-timezone Europe/Rome`) only changes how the time is displayed on the console; Linux keeps time internally in UTC. What matters is that the clock is accurate — make sure NTP is running (`timedatectl` should report `System clock synchronized: yes`).

Note that `docker compose logs` timestamps are UTC, so they will appear offset from your local time. This is expected.
