# Ignore non-runnable attestations, but require exactly one of each Linux platform.
def runnable_platforms:
    [.[] | select(.os == "linux") |
        if .architecture == "arm" then .architecture + "/" + (.variant // "")
        elif .architecture == "arm64" and ((.variant // "v8") != "v8") then "unexpected-arm64-variant"
        else .architecture end] | sort;

runnable_platforms == ["386", "amd64", "arm/v6", "arm/v7", "arm64"]
