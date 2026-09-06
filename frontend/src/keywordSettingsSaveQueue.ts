export type KeywordDeliveryMode = "comments" | "private" | "both";

export type KeywordSettingsSnapshot = {
  keywords: string[];
  minusKeywords: string[];
  sharedReply: string;
  privateReply: string;
  deliveryMode: string;
  directMessageKeywords: string[];
  revision: number;
};

export function createLatestKeywordSettingsSaveQueue(
  save: (settings: KeywordSettingsSnapshot) => Promise<KeywordSettingsSnapshot>,
  apply: (settings: KeywordSettingsSnapshot, version: number) => void,
  onError: (error: unknown) => void = () => undefined
) {
  let latestRequestID = 0;
  let exclusiveGeneration = 0;
  let tail = Promise.resolve();

  const enqueue = (settings: KeywordSettingsSnapshot, version: number): Promise<KeywordSettingsSnapshot> => {
    const requestID = ++latestRequestID;
    const generation = exclusiveGeneration;
    let persisted = false;
    const operation = tail.then(() => {
      if (generation !== exclusiveGeneration) return settings;
      persisted = true;
      return save(settings);
    });
    tail = operation.then(() => undefined, () => undefined);
    return operation.then(
      (applied) => {
        if (persisted && requestID === latestRequestID) apply(applied, version);
        return applied;
      },
      (error) => {
        if (requestID === latestRequestID) onError(error);
        throw error;
      }
    );
  };

  const runExclusive = <T>(operation: () => Promise<T>): Promise<T> => {
    const generation = ++exclusiveGeneration;
    latestRequestID += 1;
    const result = tail.then(operation);
    const guarded = result.then(
      (value) => {
        if (exclusiveGeneration === generation) exclusiveGeneration += 1;
        latestRequestID += 1;
        return value;
      },
      (error) => {
        if (exclusiveGeneration === generation) exclusiveGeneration += 1;
        latestRequestID += 1;
        throw error;
      }
    );
    tail = guarded.then(() => undefined, () => undefined);
    return guarded;
  };

  return {
    enqueue,
    invalidate: () => { latestRequestID += 1; },
    runExclusive
  };
}
