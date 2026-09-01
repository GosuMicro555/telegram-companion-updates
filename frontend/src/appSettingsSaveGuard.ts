export function createLatestAppSettingsSaveGuard<T>(
  save: (settings: T) => Promise<T>,
  apply: (settings: T) => void
) {
  let latestEditVersion = 0;

  return {
    markEdited: (version: number) => {
      latestEditVersion = Math.max(latestEditVersion, version);
    },
    save: async (settings: T, version: number): Promise<T> => {
      latestEditVersion = Math.max(latestEditVersion, version);
      const persisted = await save(settings);
      if (version === latestEditVersion) apply(persisted);
      return persisted;
    }
  };
}
