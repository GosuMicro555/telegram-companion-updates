import { describe, expect, it } from "vitest";

import { messages } from "./i18n";
import {
  currentReleaseNotes,
  markReleaseNotesSeen,
  RELEASE_NOTES,
  RELEASE_NOTES_STORAGE_KEY,
  releaseNotesForVersion,
  shouldPresentReleaseNotes
} from "./releaseNotes";
import { APP_VERSION } from "./version";

function memoryStorage() {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value)
  };
}

describe("release notes", () => {
  it("publishes 0.8.6 proxy corrective notes", () => {
    expect(APP_VERSION).toBe("0.8.6");
    const release = releaseNotesForVersion(APP_VERSION);
    expect(release?.version).toBe("0.8.6");
    expect(release?.items.ru.join(" ")).toContain("Tor");
    expect(release?.items.ru.join(" ")).toContain("Lyrebird");
    expect(release?.items.ru.join(" ")).toContain("не гарантируется");
    expect(release?.items.en.join(" ")).toContain("Embedded Tor");
    expect(release?.items.en.join(" ")).toContain("Lyrebird");
  });

  it("publishes 0.8.1 notes for keyword import and hot reply updates", () => {
    const release = releaseNotesForVersion("0.8.1");
    expect(release?.version).toBe("0.8.1");
    expect(release?.items.ru.join(" ")).toContain("Ключевые слова");
    expect(release?.items.ru.join(" ")).toContain("без остановки");
    expect(release?.items.ru.join(" ")).toContain("не отвечают друг другу");
  });

  it("publishes visible 0.8.0 notes without tdata inbox details", () => {
    const release = releaseNotesForVersion("0.8.0");
    expect(release?.version).toBe("0.8.0");
    expect(release?.items.ru).toHaveLength(7);
    expect(release?.items.en).toHaveLength(7);
    expect(release?.items.ru.join(" ")).toContain("выделенный фрагмент");
    expect(release?.items.ru.join(" ")).toContain("Удаление канала получило");
    expect(release?.items.ru.join(" ").toLocaleLowerCase("ru-RU")).not.toContain("tdata");
    expect(release?.items.en.join(" ").toLocaleLowerCase("en-US")).not.toContain("tdata");
  });

  it("presents current notes once and records acknowledgement", () => {
    const storage = memoryStorage();

    expect(shouldPresentReleaseNotes(storage)).toBe(true);
    expect(storage.getItem(RELEASE_NOTES_STORAGE_KEY)).toBeNull();
    markReleaseNotesSeen(storage);
    expect(storage.getItem(RELEASE_NOTES_STORAGE_KEY)).toBe(APP_VERSION);
    expect(shouldPresentReleaseNotes(storage)).toBe(false);
  });

  it("keeps the functional release history in descending order through 0.8.6", () => {
    expect(RELEASE_NOTES.slice(0, 7).map((release) => release.version)).toEqual(["0.8.6", "0.8.5", "0.8.3", "0.8.2", "0.8.1", "0.8.0", "0.7.0"]);
    expect(currentReleaseNotes().version).toBe("0.8.6");
  });

  it("keeps release notes localized and balanced", () => {
    for (const release of RELEASE_NOTES) {
      expect(release.title.ru).not.toBe("");
      expect(release.title.en).not.toBe("");
      expect(release.items.ru.length).toBeGreaterThanOrEqual(2);
      expect(release.items.en).toHaveLength(release.items.ru.length);
    }
  });

  it("uses a compact Russian navigation label", () => {
    expect(messages.ru.releaseNotes).toBe("Обновления");
  });
});
