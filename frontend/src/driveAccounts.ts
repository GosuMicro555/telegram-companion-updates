export type DriveImportItem = { ordinal: number; phase: string; added: number; skipped: number; error: string };
export type DriveImportBatch = { id: string; running: boolean; items: DriveImportItem[] };
export const emptyDriveBatch: DriveImportBatch = { id: "", running: false, items: [] };

export function parseDriveInput(raw: string): { links: string[]; count: number; duplicates: number; error: "" | "invalid" | "limit" } {
 const lines = raw.split(/\r?\n/).map(line => line.trim()).filter(Boolean);
 const result = { links: [] as string[], count: lines.length, duplicates: 0, error: "" as "" | "invalid" | "limit" };
 if (lines.length > 100 || raw.length > 256 * 1024) return { ...result, error: "limit" };
 const ids = new Set<string>();
 for (const line of lines) {
  try {
   if (!line.startsWith("https://drive.google.com/")) throw new Error();
   const u = new URL(line);
   if (u.hostname !== "drive.google.com" || u.username || u.password || u.hash) throw new Error();
   const params = u.searchParams;
   for (const key of params.keys()) {
    if (!["id", "export", "resourcekey", "usp"].includes(key) || params.getAll(key).length !== 1) throw new Error();
   }
   if (params.has("export") && params.get("export") !== "download") throw new Error();
   const file = /^\/file\/d\/([\w-]+)(?:\/(?:view|edit))?\/?$/.exec(u.pathname);
   const folder = /^\/drive\/folders\/([\w-]+)\/?$/.exec(u.pathname);
   const id = ["/uc", "/open"].includes(u.pathname) ? params.get("id") : file?.[1] ?? folder?.[1];
   if (!id || !/^[\w-]{1,200}$/.test(id) || (params.has("id") && params.get("id") !== id)) throw new Error();
   if (params.has("resourcekey") && !/^[\w-]{1,200}$/.test(params.get("resourcekey")!)) throw new Error();
   if (ids.has(id)) { result.duplicates++; continue; }
   ids.add(id); result.links.push(line);
  } catch { result.error = "invalid"; }
 }
 return result;
}

export const driveImportText = {
 ru: {
  title: "Добавить TData аккаунты", description: "Публичные ZIP-файлы или папки Google Drive. Одна ссылка на строку, до 100 ссылок.",
  links: "Ссылки Google Drive", start: "Скачать и добавить", cancel: "Отменить импорт", close: "Закрыть", hide: "Скрыть", completed: "Импорт завершён", duplicate: "Повторы пропущены", added: "Добавлено", skipped: "Пропущено",
  invalid: "Разрешены только корректные HTTPS-ссылки Google Drive. Исправьте отмеченные данные.", limit: "Можно вставить не более 100 ссылок.",
  fallback: "Импорт не выполнен. Повторите попытку.", connection: "Не удалось получить статус. Повторяем подключение…",
  phases: { queued: "В очереди", downloading: "Скачивание", extracting: "Распаковка", checking: "Проверка tdata", verifying: "Проверка сессии", saving: "Сохранение", added: "Добавлено", skipped: "Уже добавлено", failed: "Ошибка", cancelled: "Отменено" } as Record<string, string>,
  errors: { input_invalid: "Проверьте ссылки и лимит 100 строк.", unsafe_content: "Небезопасная структура файлов.", size_limit: "Превышен допустимый размер или число файлов.", drive_unavailable: "Нет доступа к файлам Google Drive или превышена квота скачивания.", zip_invalid: "По ссылке нет поддерживаемого ZIP-архива или папки.", tdata_invalid: "TData не найдены, повреждены или защищены паролем.", credentials_missing: "Нет app_id/app_hash: добавьте JSON с параметрами Telegram API рядом с tdata или настройте TELEGRAM_API_ID и TELEGRAM_API_HASH.", session_invalid: "Telegram не подтвердил сессию. Она недействительна или недоступна.", route_unavailable: "Настроенный маршрут Telegram недоступен.", storage_failed: "Не удалось сохранить аккаунты. Существующие аккаунты сохранены.", existing_session_unreadable: "Не удалось проверить существующие сессии на дубликаты.", import_busy: "Импорт уже выполняется.", import_unavailable: "Импорт недоступен в этой сборке." } as Record<string, string>
 },
 en: {
  title: "Add TData accounts", description: "Public Google Drive ZIP files or folders. One link per line, up to 100 links.", links: "Google Drive links", start: "Download and add", cancel: "Cancel import", close: "Close", hide: "Hide", completed: "Import complete", duplicate: "Duplicates skipped", added: "Added", skipped: "Skipped",
  invalid: "Only valid Google Drive HTTPS links are accepted.", limit: "Enter no more than 100 links.", fallback: "Import failed. Please try again.", connection: "Could not refresh status. Reconnecting…",
  phases: { queued: "Queued", downloading: "Downloading", extracting: "Extracting", checking: "Checking tdata", verifying: "Verifying session", saving: "Saving", added: "Added", skipped: "Already added", failed: "Failed", cancelled: "Cancelled" } as Record<string, string>,
  errors: { input_invalid: "Check the links and the 100-line limit.", unsafe_content: "Unsafe file structure.", size_limit: "File size or count limit exceeded.", drive_unavailable: "Google Drive access denied or download quota exceeded.", zip_invalid: "No supported ZIP or folder at this link.", tdata_invalid: "TData is missing, invalid, or password protected.", credentials_missing: "Add app_id/app_hash JSON beside tdata, or configure TELEGRAM_API_ID and TELEGRAM_API_HASH.", session_invalid: "Telegram could not verify this session.", route_unavailable: "The configured Telegram route is unavailable.", storage_failed: "Could not save accounts. Existing accounts are preserved.", existing_session_unreadable: "Could not check existing sessions for duplicates.", import_busy: "An import is already running.", import_unavailable: "Import is unavailable in this build." } as Record<string, string>
 }
};

export type DriveImportBridge = { StartDriveAccountImport(raw: string): Promise<DriveImportBatch>; GetDriveAccountImportStatus(): Promise<DriveImportBatch>; CancelDriveAccountImport(): Promise<void> };
export function driveImportBridge(): DriveImportBridge {
 const runtime = window as unknown as { go?: { wails?: { Bindings?: DriveImportBridge } } };
 const api = runtime.go?.wails?.Bindings;
 if (!api?.StartDriveAccountImport) throw new Error("import_unavailable");
 return api;
}
