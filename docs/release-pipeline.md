# Release pipeline

Use GitHub Actions or Jenkins as the Docker Hub publisher for this repository. Both run the release tests and use `ci/publish-image.sh` to publish the same five platforms and nine tags.

## What runs

For a pull request, the relevant branch is its target. Merging updates the destination branch and runs the push checks there.

| Event | README and Docker Hub table checks | Offline regression | Docker smoke tests | Push image | Sync Docker Hub overview |
|---|---|---|---|---|---|
| PR or push to any other branch | — | — | — | — | — |
| PR, push or merge to a valid version branch | Yes | Yes | — | — | — |
| PR, push or merge to `master` | Yes | Yes | Five in sequence | — | — |
| Publish a GitHub Release | Yes | Yes | Five in sequence | After all checks pass | After the image and all aliases are published |

The smoke sequence is ARMv6 → ARMv7 → ARM64 → x86 → AMD64. Each document check and offline regression can run in parallel with that sequence. GitHub Actions runs ARM64 and AMD64 natively. The other images use QEMU and a dedicated Buildx builder. The smoke containers run with no network and use the locally built images.

Version branches allow an optional `v` or `V` followed by two to four dot-separated numeric components. They can end in `build`, `audit`, `review`, `beta`, `alpha`, or `alfa`, preceded by a dot or hyphen. An optional dot or hyphen followed by one to five dot-separated numbers can follow that suffix. Version prefixes also work with subpaths such as `v4.2.6/docs/tags`. For a PR into `master`, the source branch supplies the version hint. For a version branch or numeric release tag, the first two or three components must agree with `APP_VERSION` in `versions/versions.txt`.

Both publication configurations subscribe to GitHub's `release.published` activity, including prereleases. The Git release tag selects the source commit. `versions/versions.txt` from that commit supplies the version numbers used in Docker Hub. A published prerelease follows the same sequence and updates `latest`.

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

4. In GitHub open **Releases → Draft a new release**. Select a tag that points to the prepared commit or create `v4.2.6` with `master` as its target. If the tag already exists, its commit supplies the workflows, source, versions, and Markdown.
5. Select **Publish release**, then open **Actions → Publish release to Docker Hub**. After the document, offline, and smoke jobs pass, the publisher prepares the final builder.
6. The publisher pushes one build under the first full tag, verifies the five platforms in the returned index, and assigns the remaining tags to that index in order. The last tag is `latest`. The final step uploads `DOCKERHUB.md`.
7. Check **Tags** and **Overview** on Docker Hub. If a push fails after some aliases were written, inspect the run and correct the cause before rerunning. Changing the overview is a separate operation from pushing tags.

All builds for an event use the source commit from that event. Where tag names are derived from versions/versions.txt, the file from the release commit is used even if `master` has advanced. Creating a Git tag alone leaves publishing idle. If automation publishes the GitHub Release, use a GitHub App installation token or PAT to create the release so the event can start this workflow.

[Repository secrets](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-secrets) · [GitHub release event](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#release)

## Publish with Jenkins

`Jenkinsfile` is used by two jobs. The Multibranch Pipeline handles push and pull request tests. The standalone Pipeline handles published releases. The optional GitHub Actions dispatcher only puts a release on the Jenkins queue. Jenkins runs the tests, publishes the images, and updates Docker Hub.

### 1. Prepare Jenkins and its agents

1. Install or enable **Pipeline**, **Git**, **GitHub Branch Source**, **Credentials**, **Credentials Binding**, and **Folders**. Use the Jenkins LTS and a Java version supported by that LTS on the controller and agents.
2. Configure Linux agents with a local Docker daemon and the Docker Buildx CLI plugin. Install the plugin system-wide on the publishing agent since the publication script uses a temporary `DOCKER_CONFIG`.
3. Add these labels under **Manage Jenkins → Nodes**.

   | Label | Host and dependencies |
   |---|---|
   | `certgen-ci-amd64` | AMD64, Bash, Git, Python 3.10+, g++, libyaml development headers, curl, jq, and Docker. Use four available executors across one or more agents to run both document jobs, offline regression, and the first smoke test concurrently. |
   | `certgen-ci-arm64` | ARM64 Linux with Bash, Docker and Buildx. |
   | `certgen-publish-amd64` | A trusted AMD64 agent with Bash, jq, Docker, Buildx and permission to register binfmt via a privileged container. |

4. On each AMD64 CI host, register the emulators before the first run and after a reboot if the host does not persist them.

   ```bash
   docker run --privileged --rm tonistiigi/binfmt --install arm,386
   docker run --rm tonistiigi/binfmt
   docker buildx version
   ```

   The release script registers ARM, ARM64, and x86 support on the publishing agent. Keep the publication job and its credentials restricted to trusted source. Use isolated CI agents without publication credentials for pull requests, especially when accepting fork builds.

### 2. Create the Multibranch Pipeline

1. Configure GitHub Branch Source access. For example, on GitHub use **Settings → Developer settings → GitHub Apps → New GitHub App**. Set its webhook URL to `https://your-jenkins.example/github-webhook/` and configure a shared webhook secret in GitHub and Jenkins.
2. Grant the app repository **Contents**, **Metadata**, **Pull requests**, and **Administration** read access, plus **Commit statuses** read and write if Jenkins should post statuses. Grant **Checks** read and write if using GitHub Checks. Subscribe to **Push** and **Pull request** and install the app on this repository. Configure webhook delivery so that Jenkins is reachable from GitHub. When the plugin manages repository hooks itself, its webhook permissions are needed as well.
3. Generate the app's private key and add a **GitHub App** credential with its App ID and PEM in Jenkins. Convert a PKCS#1 PEM to PKCS#8 if needed.

   ```bash
   openssl pkcs8 -topk8 -inform PEM -outform PEM -nocrypt \
     -in github-app.pem -out github-app-pkcs8.pem
   ```

4. Create a **Multibranch Pipeline** in the CI folder. Under **Branch Sources**, select **GitHub**, choose the app credential, and select `ljgonzalez1/lego-cloudflare-ssl-certgen`. Enable **Discover branches** and **Discover pull requests from origin**. For PR testing select **Merging the pull request with the current target branch revision**. Enable fork discovery only with the trust and agent isolation suitable for that code.
5. Set the script path to `Jenkinsfile`. Save and scan. Use the GitHub webhook for ongoing push/PR updates. Tag discovery is unnecessary for this job. A matching version branch runs the document checks and offline regression. The `master` suite includes the smoke tests.

[GitHub App setup for Jenkins](https://docs.cloudbees.com/docs/cloudbees-ci/latest/traditional-admin-guide/github-app-auth) · [Jenkins Multibranch Pipeline](https://www.jenkins.io/doc/book/pipeline/pipeline-as-code/)

### 3. Create the dedicated release job

1. In a restricted Jenkins folder open **Credentials → Add Credentials**, choose **Username with password**, and add two entries.

   | Credential ID | Username | Password field |
   |---|---|---|
   | `dockerhub-push` | Your Docker ID | `certgen-images` PAT |
   | `dockerhub-description` | Your Docker ID | `certgen-overview` PAT |

2. Create an ordinary **Pipeline** job named `certgen-release` in that folder. Choose **Pipeline script from SCM → Git**, repository URL `https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen.git`, branch `*/master`, and script path `Jenkinsfile`. The public repository can be fetched anonymously.
3. Save and run once with the parameters empty to initialize `RELEASE_TAG` and `RELEASE_SHA`. That run exits before tests and publication.
4. Create a dedicated Jenkins automation user. Grant it **Overall/Read** and **Job/Read** and **Job/Build** for the release job using your authorization strategy. In its user profile open **Configure → API Token → Add new Token**, generate a token, and copy it.

### 4. Connect GitHub Releases to Jenkins

1. Replace the GitHub publisher at `.github/workflows/release.yml` with the example in [jenkins-release-dispatch.yml](examples/jenkins-release-dispatch.yml) before tagging a release for Jenkins to publish. Keep one publisher in charge of the Docker Hub aliases.
2. In **Settings → Secrets and variables → Actions**, add secret `JENKINS_USER` with the automation username and secret `JENKINS_API_TOKEN` with its API token. Under **Variables** add `JENKINS_RELEASE_URL`. For a job in folder `release`, use the form `https://your-jenkins.example/job/release/job/certgen-release/buildWithParameters`.
3. Ensure the dispatch runner can reach that HTTPS URL. For a private Jenkins instance, run the dispatch job on a dedicated self-hosted runner that can reach Jenkins, by changing its `runs-on` label. The webhook used by the Multibranch Pipeline still needs a route from GitHub, or periodic repository indexing can be used for branch and PR discovery.
4. Publish the GitHub Release. The dispatcher sends its tag and exact commit SHA. Jenkins verifies the published release with the GitHub API, fetches the tag, and checks the SHA before stashing the source for all stages.
5. Follow the build in Jenkins. The document checks, **Offline tests**, and **Smoke sequence** run in parallel. **Build and push image** then creates the final Buildx environment, pushes the image and attaches aliases in order. **Update Docker Hub overview** runs after the image and all aliases are available.

To assign branch and PR tests exclusively to Jenkins, disable the GitHub workflow named **Tests** after switching the release trigger to the dispatcher. The dispatcher submits the job only. Check the final result in Jenkins.

Jenkins serializes runs within the release job. If an old release is deliberately republished or rerun, the moving aliases update in that publication. Set a tag-restriction policy in GitHub if only maintainers should be able to create the tags used for publication.

[Jenkins credential storage](https://www.jenkins.io/doc/book/managing/credentials/) · [Jenkins remote trigger with parameters](https://www.jenkins.io/doc/book/using/remote-access-api/#submitting-jobs) · [Docker Hub overview updater outside GitHub Actions](https://github.com/peter-evans/dockerhub-description#using-the-docker-image-independently-of-github-actions)

### Optional direct release webhook

If GitHub Actions should be out of the dispatch path, replace the dispatcher with a webhook receiver that can reach Jenkins.

1. In **GitHub → Settings → Webhooks → Add webhook**, point the payload URL to the receiver, choose `application/json`, set a shared secret, and subscribe to releases.
2. Validate the `X-Hub-Signature-256` header over the raw request body before parsing it. Handle only `X-GitHub-Event: release`, `action: published`, and `repository.full_name: ljgonzalez1/lego-cloudflare-ssl-certgen`.
3. Resolve `release.tag_name` to its commit, including dereferencing annotated tags. Send that tag and commit as `RELEASE_TAG` and `RELEASE_SHA` to the same authenticated Jenkins `buildWithParameters` endpoint. Treat HTTP 201 as a queued build.
4. Test delivery in GitHub's **Recent Deliveries** tab and check the Jenkins job status. Disable the GitHub publisher and dispatcher when this receiver owns the release trigger.

[Validating GitHub webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries)
