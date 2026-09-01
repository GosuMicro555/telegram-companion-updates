import { describe, expect, test } from "vitest";
import { createLatestAppSettingsSaveGuard } from "./appSettingsSaveGuard";

type AppSettingsSnapshot = {
  joinIntervalMinMinutes: number;
  joinIntervalMaxMinutes: number;
  revision: number;
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise; });
  return { promise, resolve };
}

describe("paced channel join settings persistence", () => {
  test("retains an edit made while an earlier save is pending", async () => {
    const firstResponse = deferred<AppSettingsSnapshot>();
    const secondResponse = deferred<AppSettingsSnapshot>();
    const requests: AppSettingsSnapshot[] = [];
    let visible = { joinIntervalMinMinutes: 10, joinIntervalMaxMinutes: 30, revision: 0 };
    const guard = createLatestAppSettingsSaveGuard<AppSettingsSnapshot>(
      (settings) => {
        requests.push(settings);
        return requests.length === 1 ? firstResponse.promise : secondResponse.promise;
      },
      (settings) => { visible = settings; }
    );

    const firstSave = guard.save(visible, 1);
    visible = { ...visible, joinIntervalMaxMinutes: 45 };
    guard.markEdited(2);

    firstResponse.resolve({ joinIntervalMinMinutes: 10, joinIntervalMaxMinutes: 30, revision: 1 });
    await firstSave;

    expect(visible).toEqual({ joinIntervalMinMinutes: 10, joinIntervalMaxMinutes: 45, revision: 0 });

    const secondSave = guard.save(visible, 2);
    secondResponse.resolve({ joinIntervalMinMinutes: 10, joinIntervalMaxMinutes: 45, revision: 2 });
    await secondSave;

    expect(requests).toEqual([
      { joinIntervalMinMinutes: 10, joinIntervalMaxMinutes: 30, revision: 0 },
      { joinIntervalMinMinutes: 10, joinIntervalMaxMinutes: 45, revision: 0 }
    ]);
    expect(visible).toEqual({ joinIntervalMinMinutes: 10, joinIntervalMaxMinutes: 45, revision: 2 });
  });
});
