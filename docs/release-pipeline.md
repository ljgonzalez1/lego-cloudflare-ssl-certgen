# Release pipeline

The [CI/CD workflow](../.github/workflows/tests.yml) validates branch changes and publishes releases. It uses `ci/publish-image.sh` to publish five platforms and nine tags to Docker Hub.

When applying the v4.2.11 hotfix over v4.2.9, remove `.github/workflows/release.yml`. The release trigger now lives in `tests.yml`.

## What runs

For a pull request, the relevant branch is its target. Merging updates the destination branch and runs the push checks there.

| Event | README and Docker Hub table checks | Offline regression | Docker smoke tests | Push image | Sync Docker Hub overview |
|---|---|---|---|---|---|
| PR or push to any other branch | — | — | — | — | — |
| PR targeting a valid version branch | Yes | Yes | Five in parallel | — | — |
| Push or merge to a valid version branch | Yes | Yes | — | — | — |
| PR, push or merge to `master` | Yes | Yes | Five in parallel | — | — |
| Publish a GitHub Release | Yes | Yes | Five in parallel | After all checks pass | After the image and all aliases are published |

The two document checks run in parallel. Offline regression starts when both pass. On qualifying pull requests, pushes to `master`, and releases, ARMv6, ARMv7, ARM64, i386 and AMD64 smoke tests then run in parallel. A merge to `master` runs through the push event. GitHub Actions runs ARM64 and AMD64 natively. The other platforms use QEMU and a dedicated Buildx builder. The smoke containers run with no network and use the locally built images.

```mermaid
flowchart TD
    readme["Check README.md"] --> offline["Offline tests"]
    dockerhub["Check DOCKERHUB.md"] --> offline
    offline --> v6["ARMv6 smoke test"]
    offline --> v7["ARMv7 smoke test"]
    offline --> arm64["ARM64 smoke test"]
    offline --> i386["i386 smoke test"]
    offline --> amd64["AMD64 smoke test"]
    v6 --> release{"Published GitHub release?"}
    v7 --> release
    arm64 --> release
    i386 --> release
    amd64 --> release
    release -->|Yes| push["Buildx and push"]
    push --> wait_image["Wait 30 seconds for image propagation"]
    wait_image --> tag["Check version tag and digest"]
    push --> update["Update Docker Hub overview"]
    update --> wait_description["Wait 30 seconds for overview propagation"]
    wait_description --> description["Check published overview"]
```

Version branches allow an optional `v` or `V` followed by two to four dot-separated numeric components. They can end in `build`, `audit`, `review`, `beta`, `alpha`, or `alfa`, preceded by a dot or hyphen. An optional dot or hyphen followed by one to five dot-separated numbers can follow that suffix. Version prefixes also work with subpaths such as `v4.2.11/docs/tags`. For a PR into `master`, the source branch supplies the version hint. For a version branch or numeric release tag, the first two or three components must agree with `APP_VERSION` in `versions/versions.txt`.

The publishing workflow subscribes to GitHub's `release.published` activity, including prereleases. The Git release tag selects the source commit. `versions/versions.txt` from that commit supplies the version numbers used in Docker Hub. A published prerelease follows the same sequence and updates `latest`.

## Update the Markdown tables

1. Update `versions/versions.txt` for the upcoming image and append its row to `versions/versions.csv`. Keep the upcoming release as the final CSV row.
2. From the repository root, regenerate the short table in `DOCKERHUB.md` and the full history in `README.md`.

   ```bash
   python3 versions/tags_table.py
   ```

3. Commit the version files and both Markdown files before you open the PR or publish the release. The document jobs verify that the final CSV row matches the image tags from `versions/versions.txt` and that each committed table matches the CSV. Release publication starts after both documents, offline regression, and all five smoke tests pass.

## Set up Docker Hub access

1. Sign in to [Docker Home](https://app.docker.com/) with a Docker ID that can push to and edit `ljgonzalez/lego-cloudflare-certgen`. Verify the account email if Docker requires it for personal access tokens.
2. Open your avatar menu, choose **Account settings → Personal access tokens → Generate new token**, and name the token `certgen-images`. Set an expiry and choose **Repo Read & Write**. Generate and copy the token.
3. Make a second token named `certgen-overview` and choose **Repo Read, Write, Delete**, the scope required by the overview updater. Copy this value separately.
4. Review the repository's tag immutability settings. The pipeline updates shared aliases including `v4.2`, `v4`, and `latest`. Any shared aliases covered by immutability rules will cause publication to fail.

[Docker PAT setup](https://docs.docker.com/security/access-tokens/personal-access-tokens/) · [Repository access scopes](https://docs.docker.com/security/access-tokens/reference/) · [Overview updater](https://github.com/peter-evans/dockerhub-description#action-inputs)

## Publish with GitHub Actions

1. In your GitHub repository go to **Settings → Secrets and variables → Actions → Secrets → New repository secret**. Add these values separately.

   | Secret | Value |
   |---|---|
   | `DOCKERHUB_USERNAME` | Your Docker ID, normally `ljgonzalez` |
   | `DOCKERHUB_PUSH_TOKEN` | The `certgen-images` PAT |
   | `DOCKERHUB_DESCRIPTION_TOKEN` | The `certgen-overview` PAT |

2. Update the version files and generate both Markdown tables. Edit any other Docker Hub overview text in `DOCKERHUB.md`. The file must be at or below 25,000 bytes.
3. Check the Docker tags locally from the repository root.

   ```bash
   bash ci/publish-image.sh --print-tags
   ```

4. In GitHub open **Releases → Draft a new release**. Select a tag that points to the prepared commit or create `v4.2.11` with `master` as its target. If the tag already exists, its commit supplies the workflows, source, versions, and Markdown.
5. Select **Publish release**, then open the release run under **Actions → CI/CD**. After the document, offline, and five smoke jobs pass, the publisher prepares the final builder.
6. The publisher verifies the five platforms in the built index and assigns all tags, with `latest` last. After the push, the overview update and the 30-second image propagation wait start independently. The tag check hashes the index served by the public Docker registry and compares it with the digest from the build.
7. After the overview update, its separate 30-second wait precedes the Markdown comparison against `DOCKERHUB.md`. The comparison tolerates CRLF and trailing whitespace at the end. Each verification makes up to five attempts with 15 seconds between attempts and fails if the published data still differs.

All builds for an event use the source commit from that event. Where tag names are derived from versions/versions.txt, the file from the release commit is used even if `master` has advanced. Creating a Git tag alone leaves publishing idle. If automation publishes the GitHub Release, use a GitHub App installation token or PAT to create the release so the event can start this workflow.

[Repository secrets](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-secrets) · [GitHub release event](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#release)
