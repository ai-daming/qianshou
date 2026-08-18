import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative, resolve } from "node:path";

const repository = resolve(import.meta.dirname, "..");
const expected = join(repository, "packages/api-client/src/generated");
const temporary = mkdtempSync(join(tmpdir(), "qianshou-api-client-"));

function files(directory) {
  const result = new Map();
  const visit = (current) => {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const path = join(current, entry.name);
      if (entry.isDirectory()) visit(path);
      else result.set(relative(directory, path), readFileSync(path, "utf8"));
    }
  };
  visit(directory);
  return result;
}

try {
  const pnpm = process.platform === "win32" ? "pnpm.cmd" : "pnpm";
  const generated = spawnSync(
    pnpm,
    [
      "--filter",
      "@qianshou/api-client",
      "exec",
      "openapi-ts",
      "-i",
      join(repository, "protocol/openapi.yaml"),
      "-o",
      temporary,
    ],
    { cwd: repository, stdio: "inherit" },
  );
  if (generated.status !== 0) process.exit(generated.status ?? 1);
  const formatted = spawnSync(pnpm, ["exec", "biome", "format", "--write", temporary], {
    cwd: repository,
    stdio: "inherit",
  });
  if (formatted.status !== 0) process.exit(formatted.status ?? 1);

  const actualFiles = files(temporary);
  const expectedFiles = files(expected);
  if (
    actualFiles.size !== expectedFiles.size ||
    [...actualFiles].some(([name, body]) => expectedFiles.get(name) !== body)
  ) {
    console.error("Generated API client is stale. Run: pnpm api:generate");
    process.exit(1);
  }
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
