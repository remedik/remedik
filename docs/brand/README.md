# Brand assets

| File | Use |
| --- | --- |
| `remedik-banner.png` | The README header, and anywhere the project introduces itself |
| `remedik-mark.png` | The Helm chart icon, the dashboard favicon, and the GitHub organisation avatar |
| `remedik-avatar-dark.png` | Avatars and social cards on light backgrounds |
| `remedik-avatar-light.png` | The same, on dark backgrounds |

The mark is a branch that rejoins: an alert diverges into a remediation and
the workload comes back. The green node is the one that ends well, and it is
the only colour in the mark, because most of what remedik does is decide
*not* to act.

Two things depend on these paths, so moving a file is not a rename:

- `charts/remedik/Chart.yaml` points its `icon:` at the raw URL of
  `remedik-mark.png` on `main`. Helm registries and Artifact Hub fetch it
  over the public internet, so it resolves only once this repository is
  public.
- `README.md` embeds the banner by the same kind of URL.
