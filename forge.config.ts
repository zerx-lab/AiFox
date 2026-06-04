import type { ForgeConfig } from "@electron-forge/shared-types";
import { MakerZIP } from "@electron-forge/maker-zip";
import { MakerSquirrel } from "@electron-forge/maker-squirrel";
import { MakerDeb } from "@electron-forge/maker-deb";
import { MakerRpm } from "@electron-forge/maker-rpm";
import { MakerAppImage } from "@reforged/maker-appimage";
import { VitePlugin } from "@electron-forge/plugin-vite";
import { AutoUnpackNativesPlugin } from "@electron-forge/plugin-auto-unpack-natives";
import { FusesPlugin } from "@electron-forge/plugin-fuses";
import { FuseV1Options, FuseVersion } from "@electron/fuses";
import { promises as fs } from "node:fs";
import { join } from "node:path";

// Locales we keep in the packaged app. Chromium *requires* en-US.pak as a
// fallback even if the UI is in another language — removing it breaks string
// lookups and shows boxes. zh-CN.pak covers the project's primary audience.
// To add a locale: drop its <lang>.pak name in here, no other change needed.
const KEEP_LOCALES = new Set(["en-US.pak", "zh-CN.pak"]);

// Electron Packager `afterExtract` hook: runs once Electron has been unpacked
// into the output dir, BEFORE app code is copied in. This is the right place
// to delete bundled-Electron files (locales, license dumps) because we're
// editing the Electron distribution itself, not our app's resources.
//
// Layout by platform (buildPath points at the dir below):
//   linux/win32  buildPath/locales/*.pak              buildPath/LICENSES.chromium.html
//   darwin       buildPath/Electron.app/Contents/Frameworks/Electron Framework.framework/
//                Versions/A/Resources/*.pak           and a LICENSES.chromium.html sibling
//
// We probe both layouts and silently skip whichever doesn't exist.
async function trimElectronExtras(buildPath: string): Promise<void> {
  const localesCandidates = [
    join(buildPath, "locales"),
    join(
      buildPath,
      "Electron.app",
      "Contents",
      "Frameworks",
      "Electron Framework.framework",
      "Versions",
      "A",
      "Resources",
    ),
  ];
  for (const dir of localesCandidates) {
    let entries: string[];
    try {
      entries = await fs.readdir(dir);
    } catch {
      continue;
    }
    await Promise.all(
      entries
        .filter((n) => n.endsWith(".pak") && !KEEP_LOCALES.has(n))
        .map((n) => fs.rm(join(dir, n), { force: true })),
    );
  }

  // LICENSES.chromium.html is a ~20 MB legal-attribution dump that Chromium
  // ships next to the binary. Attribution still lives in the repo LICENSE
  // and can be surfaced via an About dialog when one is built — there's no
  // legal need to keep it at the app root as a giant HTML file.
  const licenseCandidates = [
    join(buildPath, "LICENSES.chromium.html"),
    join(
      buildPath,
      "Electron.app",
      "Contents",
      "Frameworks",
      "LICENSES.chromium.html",
    ),
  ];
  await Promise.all(licenseCandidates.map((p) => fs.rm(p, { force: true })));
}

// Forge config for the Relay Electron shell.
//
// The Go backend is shipped as a sidecar binary under `bin/<platform-arch>/`
// and copied into the app's resources at package time via `extraResource`.
// Build that binary with `task build:go` before running `task build`.
const config: ForgeConfig = {
  packagerConfig: {
    asar: true,
    // `icon` is base path; packager appends .png/.ico/.icns per platform.
    icon: "./assets/logo/png/relay-512",
    extraResource: ["./bin", "./assets/logo"],
    // Strip ~65 MB of unused locale .pak files and the 20 MB Chromium
    // license dump from the packaged app. See trimElectronExtras above.
    afterExtract: [
      (buildPath, _electronVersion, _platform, _arch, callback) => {
        trimElectronExtras(buildPath).then(
          () => callback(),
          (err: unknown) =>
            callback(err instanceof Error ? err : new Error(String(err))),
        );
      },
    ],
  },
  rebuildConfig: {},
  // Per-platform makers. Electron Forge invokes each maker only on its
  // declared platforms, so a single `task make` run on a given OS produces
  // exactly the artifacts that OS can build.
  //
  //   linux   -> .deb / .rpm (system-installed: register .desktop + hicolor
  //              icons + /usr/bin shim via apt/dpkg/dnf) and AppImage
  //              (portable single-file executable, no system registration)
  //   win32   -> Squirrel.Windows (RELEASES + .nupkg + Setup.exe; auto-update capable)
  //   darwin  -> ZIP (no signing identity wired in yet)
  //
  // ZIP is also kept as a portable fallback on linux/win32.
  //
  // Desktop integration: the AppImage is portable and does NOT add a
  // launcher entry to the application menu on its own (that needs external
  // tooling like AppImageLauncher). The .deb/.rpm packages DO — their
  // post-install installs /usr/share/applications/ai-fox.desktop and the
  // hicolor icon set, so the app shows up in the menu like a native app.
  // Arch users are covered by the separate `task build:arch` path
  // (scripts/make-arch.sh), since Forge has no pacman maker.
  makers: [
    new MakerAppImage({
      options: {
        // Forge auto-discovers icon/categories from `extraResource` + package.json,
        // but being explicit avoids surprises across maker versions.
        icon: "./assets/logo/png/relay-512.png",
        categories: ["Utility"],
      },
    }),
    // electron-installer-{debian,redhat} generate the .desktop entry, install
    // the icon under hicolor (named after `name`, so Icon= resolves), and drop
    // a /usr/bin/ai-fox launcher. The .desktop basename (ai-fox.desktop) equals
    // app.setName("ai-fox") in main.ts, so Wayland app_id matching just works.
    new MakerDeb({
      options: {
        name: "ai-fox",
        productName: "AiFox",
        genericName: "AI Traffic Debugger",
        description:
          "Debug and visualize AI/LLM HTTP traffic, prompts, MCP calls and tool calls.",
        icon: "./assets/logo/png/relay-512.png",
        categories: ["Development", "Utility"],
        bin: "ai-fox",
        section: "devel",
        maintainer: "zerx-lab",
      },
    }),
    new MakerRpm({
      options: {
        name: "ai-fox",
        productName: "AiFox",
        genericName: "AI Traffic Debugger",
        description:
          "Debug and visualize AI/LLM HTTP traffic, prompts, MCP calls and tool calls.",
        icon: "./assets/logo/png/relay-512.png",
        categories: ["Development", "Utility"],
        bin: "ai-fox",
        license: "MIT",
      },
    }),
    new MakerSquirrel({
      // `name` becomes the Squirrel app id; must be a valid file/dir token.
      name: "relay",
      // setupIcon requires .ico; we only ship PNGs today, so fall back to
      // Forge's default until an .ico is added under assets/logo/.
    }),
    new MakerZIP({}, ["darwin", "linux", "win32"]),
  ],
  plugins: [
    new AutoUnpackNativesPlugin({}),
    new VitePlugin({
      build: [
        {
          entry: "electron/src/main/main.ts",
          config: "vite.main.config.ts",
          target: "main",
        },
        {
          entry: "electron/src/preload/preload.ts",
          config: "vite.preload.config.ts",
          target: "preload",
        },
      ],
      renderer: [
        {
          name: "main_window",
          config: "vite.renderer.config.ts",
        },
      ],
    }),
    // Recommended Electron hardening. See:
    // https://www.electronjs.org/docs/latest/tutorial/fuses
    new FusesPlugin({
      version: FuseVersion.V1,
      [FuseV1Options.RunAsNode]: false,
      [FuseV1Options.EnableCookieEncryption]: true,
      [FuseV1Options.EnableNodeOptionsEnvironmentVariable]: false,
      [FuseV1Options.EnableNodeCliInspectArguments]: false,
      [FuseV1Options.EnableEmbeddedAsarIntegrityValidation]: true,
      [FuseV1Options.OnlyLoadAppFromAsar]: true,
    }),
  ],
};

export default config;
