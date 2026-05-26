# FAQ

## Is a satellite forced to follow the parent's prefix for its node TGs?

**No.** Nodes on a satellite can select and monitor any TG they like. The parent's prefix only governs *routing*: a TG whose number falls under the parent's prefix is eligible to be carried out over the parent's trunk links to the wider mesh. TGs outside that range stay local to the parent + its satellites.

Cross-satellite communication still works either way — any TG keyed on one satellite is relayed to every other satellite of the same parent, so nodes on different satellites can always talk to each other regardless of TG number.

**Big caveat:** the satellite admin can apply a TG filter on their end, which narrows the set of TGs carried in and out of that satellite. If a satellite seems to be missing traffic, check its filter first — your mileage will vary based on how the owner has scoped it.
