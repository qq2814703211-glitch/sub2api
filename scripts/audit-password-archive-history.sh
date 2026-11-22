#!/bin/sh
# Read commit trees only. Exit 0 for no matches, 1 for matches, 2 for an incomplete audit.
set -u
fail() { printf '%s\n' "$1" >&2; exit 2; }
[ "$#" -ge 1 ] || fail 'Usage: sh audit-password-archive-history.sh MIRROR [EXTRA_REPO_RELATIVE_DIRECTORY ...]'
mirror=$1
shift
audit_git() { git --no-replace-objects --literal-pathspecs -C "$mirror" "$@"; }
[ "$(audit_git rev-parse --is-bare-repository)" = true ] || fail 'Expected a bare mirror.'
[ "$(audit_git rev-parse --is-shallow-repository)" = false ] || fail 'Cannot audit a shallow history.'
heads=$(audit_git for-each-ref --format='%(refname)' refs/heads) || fail 'Cannot enumerate branches.'
[ -n "$heads" ] || fail 'No branch heads to audit.'
for directory in "$@"; do
    case "/$directory/" in *'//'*|*'/../'*|*'/./'*) fail 'Use repository-relative directory names without dot or empty components.' ;; esac
done
match=$(audit_git rev-list --branches --tags --full-history --max-count=1 -- data/password-copies "$@") || fail 'Commit traversal failed.'
if [ -n "$match" ]; then
    printf '%s\n' 'Tracked archive path found in branch or tag history. Restrict access and investigate privately.'
    exit 1
fi
printf '%s\n' 'No tracked entry at the checked archive paths in the mirrored branch and tag histories.'
