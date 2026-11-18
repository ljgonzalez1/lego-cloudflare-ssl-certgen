// Use this file in both the Multibranch Pipeline and the dedicated release job.
def releaseJob = !env.BRANCH_NAME
def releaseTag = ''
def releaseSha = ''
def targetBranch = env.CHANGE_TARGET ?: env.BRANCH_NAME
def fullSuite = releaseJob || targetBranch == 'master'

if (releaseJob) {
    properties([
        disableConcurrentBuilds(),
        parameters([
            string(name: 'RELEASE_TAG', defaultValue: '', description: 'Tag of a published GitHub Release'),
            string(name: 'RELEASE_SHA', defaultValue: '', description: 'Commit SHA from the GitHub release event')
        ])
    ])

    releaseTag = params.RELEASE_TAG?.trim() ?: ''
    releaseSha = params.RELEASE_SHA?.trim() ?: ''
    if (!releaseTag && !releaseSha) {
        echo 'Release parameters initialized. Waiting for a published GitHub Release.'
        return
    }
    if (!releaseTag || !(releaseSha ==~ /[0-9a-f]{40}/)) {
        error 'Expected a release tag and its full commit SHA.'
    }
} else if (env.TAG_NAME || !targetBranch ||
           (!fullSuite && !(targetBranch.split('/', 2)[0] ==~ /[vV]?[0-9]+\.[0-9].*/))) {
    echo 'No tests for this ref.'
    return
}

def eligible = false
stage('Resolve source') {
    node('certgen-ci-amd64') {
        deleteDir()
        if (releaseJob) {
            withEnv(["RELEASE_TAG=${releaseTag}", "RELEASE_SHA=${releaseSha}"]) {
                sh label: 'Verify the published release and fetch its commit', script: '''#!/usr/bin/env bash
set -euo pipefail

repo=ljgonzalez1/lego-cloudflare-ssl-certgen
git check-ref-format "refs/tags/$RELEASE_TAG"
encoded_tag=$(jq -nr --arg tag "$RELEASE_TAG" '$tag | @uri')
curl --fail --silent --show-error --max-time 30 \
    -H 'Accept: application/vnd.github+json' \
    "https://api.github.com/repos/$repo/releases/tags/$encoded_tag" |
    jq -e --arg tag "$RELEASE_TAG" '
        .tag_name == $tag and .draft == false and (.published_at | type) == "string"
    ' >/dev/null
git init -q
git fetch --no-tags --depth=1 "https://github.com/$repo.git" \
    "refs/tags/$RELEASE_TAG:refs/tags/$RELEASE_TAG"
git checkout --detach "refs/tags/$RELEASE_TAG"
if [[ "$(git rev-parse HEAD)" != "$RELEASE_SHA" ]]; then
    printf 'Release tag no longer points to the commit from the release event\n' >&2
    exit 1
fi
'''
            }
            eligible = true
        } else {
            checkout scm
            withEnv(["TARGET_BRANCH=${targetBranch}"]) {
                eligible = fullSuite || sh(
                    script: 'bash ci/is-version-branch.sh "$TARGET_BRANCH"',
                    returnStatus: true
                ) == 0
            }
        }
        if (eligible) {
            stash name: 'certgen-source', excludes: '.git,.git/**'
        }
    }
}

if (!eligible) {
    echo 'No tests for this version branch name.'
    return
}

def checks = [
    'Offline tests': {
        stage('Offline tests') {
            node('certgen-ci-amd64') {
                deleteDir()
                unstash 'certgen-source'
                sh 'sh tests/run.sh'
            }
        }
    }
]

def versionRef = releaseJob ? releaseTag : (targetBranch == 'master' ? (env.CHANGE_BRANCH ?: targetBranch) : targetBranch)
['DOCKERHUB.md', 'README.md'].each { document ->
    checks["Check version in ${document}"] = {
        stage("Check version in ${document}") {
            node('certgen-ci-amd64') {
                deleteDir()
                unstash 'certgen-source'
                withEnv(["DOCUMENT=${document}", "VERSION_REF=${versionRef}"]) {
                    sh 'python3 -B versions/check_version.py "$DOCUMENT" --ref "$VERSION_REF"'
                }
            }
        }
    }
}

if (fullSuite) {
    checks['Smoke sequence'] = {
        for (String platform : ['linux/arm/v6', 'linux/arm/v7', 'linux/arm64', 'linux/386', 'linux/amd64']) {
            stage("Smoke ${platform}") {
                node(platform == 'linux/arm64' ? 'certgen-ci-arm64' : 'certgen-ci-amd64') {
                    deleteDir()
                    unstash 'certgen-source'
                    withEnv(["SMOKE_PLATFORM=${platform}"]) {
                        sh 'bash ci/run-smoke.sh "$SMOKE_PLATFORM"'
                    }
                }
            }
        }
    }
}
parallel checks

if (releaseJob) {
    node('certgen-publish-amd64') {
        deleteDir()
        unstash 'certgen-source'
        stage('Build and push image') {
            withCredentials([usernamePassword(
                credentialsId: 'dockerhub-push',
                usernameVariable: 'DOCKERHUB_USERNAME',
                passwordVariable: 'DOCKERHUB_PUSH_TOKEN'
            )]) {
                sh 'PUBLISH_RESULT_FILE=.publish-result.json bash ci/publish-image.sh'
            }
        }
        parallel(
            'Publish and verify overview': {
                stage('Update Docker Hub overview') {
                    withCredentials([usernamePassword(
                        credentialsId: 'dockerhub-description',
                        usernameVariable: 'DOCKERHUB_USERNAME',
                        passwordVariable: 'DOCKERHUB_PASSWORD'
                    )]) {
                        sh '''
docker run --rm \
    --mount "type=bind,source=$PWD/DOCKERHUB.md,target=/workspace/DOCKERHUB.md,readonly" \
    -e DOCKERHUB_USERNAME -e DOCKERHUB_PASSWORD \
    -e DOCKERHUB_REPOSITORY=ljgonzalez/lego-cloudflare-certgen \
    -e README_FILEPATH=/workspace/DOCKERHUB.md \
    peterevans/dockerhub-description:5.0.0
'''
                    }
                }
                stage('Verify published Docker Hub overview') {
                    sh 'bash ci/check-dockerhub.sh description'
                }
            },
            'Verify image': {
                stage('Verify published version tag') {
                    sh '''
image=$(jq -er .image .publish-result.json)
digest=$(jq -er .digest .publish-result.json)
bash ci/check-dockerhub.sh image "$image" "$digest"
'''
                }
            }
        )
    }
}
