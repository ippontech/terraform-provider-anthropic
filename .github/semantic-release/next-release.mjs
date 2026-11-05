// Prints the version bump that semantic-release would cut from the commits since the last release
// tag, for the `pending` job of semantic-release.yml: `last_tag=<tag or empty>` and
// `release_type=<major|minor|patch|none>` on stdout, one per line; the analysis log goes to stderr.
//
// It runs the same @semantic-release/commit-analyzer plugin, with the configuration .releaserc gives
// it, as the release itself: the two cannot disagree on a commit (a revert, a `BREAKING-CHANGE:`
// footer, a rule added to .releaserc). `semantic-release --dry-run` would answer the same question,
// but it first checks that it may push to the repository (lib/git.js, verifyAuth), before any
// plugin runs, so it needs the App token, which only the release job holds.
//
// Run it from the repository root, after `npm ci` in this directory:
//   node .github/semantic-release/next-release.mjs

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { format } from "node:util";
import { analyzeCommits } from "@semantic-release/commit-analyzer";

const ANALYZER = "@semantic-release/commit-analyzer";
const RECORD = "\u001e";
const FIELD = "\u001f";

function git(...args) {
  return execFileSync("git", args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
}

// The commit-analyzer entry of .releaserc (JSON here), or an empty configuration when the file lists
// the plugin without options or leaves it out: semantic-release then runs it with its defaults.
function releaseConfig() {
  const { plugins = [], tagFormat = "v${version}" } = JSON.parse(readFileSync(".releaserc", "utf8"));
  const entry = plugins.map((plugin) => (Array.isArray(plugin) ? plugin : [plugin, {}])).find(([name]) => name === ANALYZER);
  return { tagFormat, pluginConfig: (entry && entry[1]) || {} };
}

// The highest release tag merged into HEAD, as semantic-release picks the last release of a
// release branch (lib/get-last-release.js): a tag in the `tagFormat` shape carrying a stable
// version, prerelease tags left out.
function lastTag(tagFormat) {
  const pattern = tagFormat.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace("\\$\\{version\\}", "(\\d+)\\.(\\d+)\\.(\\d+)");
  const tagRegexp = new RegExp(`^${pattern}$`);
  const releases = git("tag", "--merged", "HEAD")
    .split("\n")
    .map((tag) => {
      const match = tag.match(tagRegexp);
      return match && { tag, version: match.slice(1, 4).map(Number) };
    })
    .filter(Boolean)
    .sort((a, b) => b.version[0] - a.version[0] || b.version[1] - a.version[1] || b.version[2] - a.version[2]);
  return releases.length > 0 ? releases[0].tag : "";
}

// The commits since the tag, newest first, in the shape semantic-release hands the plugin
// (lib/git.js, getCommits): the hash and the whole trimmed message.
function commitsSince(tag) {
  return git("log", `--format=%H${FIELD}%B${RECORD}`, tag ? `${tag}..HEAD` : "HEAD")
    .split(RECORD)
    .map((record) => record.trim())
    .filter(Boolean)
    .map((record) => {
      const [hash, ...message] = record.split(FIELD);
      return { hash, message: message.join(FIELD).trim() };
    });
}

const { tagFormat, pluginConfig } = releaseConfig();
const tag = lastTag(tagFormat);
const commits = commitsSince(tag);
const logger = { log: (...args) => console.error(format(...args)) };
const releaseType = await analyzeCommits(pluginConfig, { commits, logger, cwd: process.cwd() });

console.log(`last_tag=${tag}`);
console.log(`release_type=${releaseType || "none"}`);
