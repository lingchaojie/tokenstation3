#!/usr/bin/env bash
set -euo pipefail
: "${GITHUB_EVENT_NAME:?}" "${GITHUB_REF:?}" "${GITHUB_OUTPUT:?}" "${REQUESTED_REF:?}"

tag_name=$REQUESTED_REF
branch_release=false
if [[ "$GITHUB_EVENT_NAME" == push && "$GITHUB_REF" == refs/heads/release ]]; then
  : "${GITHUB_RUN_NUMBER:?}"
  [[ "$GITHUB_RUN_NUMBER" =~ ^[0-9]+$ ]]
  tag_name="v0.1000.$GITHUB_RUN_NUMBER"
  branch_release=true
  source_sha=$(git rev-parse HEAD)
  if git show-ref --verify --quiet "refs/tags/$tag_name"; then
    tagged_sha=$(git rev-parse "refs/tags/$tag_name^{commit}")
    if [[ "$tagged_sha" != "$source_sha" ]]; then
      echo "Existing release tag $tag_name points to another source commit" >&2
      exit 1
    fi
  else
    git -c user.name='github-actions[bot]' \
      -c user.email='41898282+github-actions[bot]@users.noreply.github.com' \
      tag -a "$tag_name" "$source_sha" -m 'Custom release from release'
    git push origin "refs/tags/$tag_name"
  fi
fi
{
  echo "tag=$tag_name"
  echo "branch_release=$branch_release"
} >> "$GITHUB_OUTPUT"
