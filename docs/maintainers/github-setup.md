# GitHub setup runbook

Everything here is done **once**, by the maintainer, after saying "publish".
Replace `OWNER` with your GitHub user name in these files first:
`README.md`, `.github/CODEOWNERS`, `.github/ISSUE_TEMPLATE/config.yml`,
`site/index.html`.

> ⚠️ The commands below were written without access to your GitHub account and
> were not executed. Run them one by one and read the output. Workflow action
> versions and tool versions were written from memory without registry access;
> the first CI run is the real test.

## 0. Name check

The name `dricket` as an *organization* is taken; use a repository under your
own account.

```
gh repo view OWNER/dricket          # should say: not found
```

Not checked: Go module proxy, Homebrew, domains, trademarks. Check them before
announcing the project.

## 1. Create the repository (public)

```
gh repo create OWNER/dricket --public \
  --description "Git-native ticketing for humans and AI agents. Claims by push, no server." \
  --homepage "https://OWNER.github.io/dricket/"
git remote add origin git@github.com:OWNER/dricket.git
git push -u origin main
```

Topics:

```
gh repo edit OWNER/dricket --add-topic git --add-topic ticketing \
  --add-topic ai-agents --add-topic cli --add-topic golang
```

## 2. Security features

```
# Private vulnerability reporting
gh api -X PUT repos/OWNER/dricket/private-vulnerability-reporting
# Dependency alerts and automated security updates
gh api -X PUT repos/OWNER/dricket/vulnerability-alerts
gh api -X PUT repos/OWNER/dricket/automated-security-fixes
# Least-privilege default token for workflows
gh api -X PUT repos/OWNER/dricket/actions/permissions/workflow \
  -f default_workflow_permissions=read -F can_approve_pull_request_reviews=false
```

## 3. Rulesets

```
gh api -X POST repos/OWNER/dricket/rulesets --input .github/rulesets/main.json
gh api -X POST repos/OWNER/dricket/rulesets --input .github/rulesets/release-tags.json
```

`main` then requires a pull request, a green `ci` check, linear history, and
forbids force-push and deletion. The review count is 0 so a single maintainer
can merge their own pull requests; raise it when there are more maintainers.
There is no bypass actor on purpose. If you lock yourself out, delete or edit
the ruleset in *Settings → Rules*.

The `ci` check only exists after the workflow has run once. Push the workflow on
a branch, open a pull request, let it run, then create the rulesets.

## 4. Pages (landing page)

```
gh api -X POST repos/OWNER/dricket/pages -f build_type=workflow
```

The `pages` workflow publishes `site/` on every push to `main`.

## 5. Pin actions by SHA

The workflows use version tags. Before the first release, replace each tag with
the commit SHA it points to (keep the tag as a comment). Dependabot then keeps
the SHAs current. A tool such as `pinact` can do this in one pass.

## 6. First release

1. All M1 acceptance tests pass, `CHANGELOG.md` updated.
2. `git tag -s v0.1.0 -m "v0.1.0" && git push origin v0.1.0`
3. The `release` workflow builds the archives, `checksums.txt` and a build
   provenance attestation. Mark the project as alpha in the release notes.
4. Verify a download: checksum and `gh attestation verify <file> --owner OWNER`.

## 7. Ongoing

- Review Dependabot pull requests weekly.
- Keep `docs/protocol.md` in sync with the code. It is the contract.
