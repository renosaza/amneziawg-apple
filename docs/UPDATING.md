# Updating from Amnezia upstream

Run the update helper from a clean checkout on the branch that carries the patch series:

```sh
scripts/update-amnezia-upstream.sh master
```

It fetches `amnezia` and `wireguard`, creates a local `update/<date>-<sha>` branch, rebases the commits after the old merge base onto `amnezia/master`, runs the build and tests, and prints a `git range-diff`. It never pushes or force-pushes.

If rebase stops for a conflict, resolve the behavior semantically, then continue with `git rebase --continue`. To abandon only the local update attempt, use `git rebase --abort` and switch back to the source branch. Do not merge upstream directly into the patched branch.

After a successful replay, review the range-diff, run the real macOS multi-tunnel smoke tests, then update `docs/UPSTREAM.md` with the new base, reference status, local patch list, and any remaining divergence.
