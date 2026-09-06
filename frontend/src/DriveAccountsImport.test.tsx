import { expect, test } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { DriveAccountsDialog } from "./DriveAccountsImport";
import { emptyDriveBatch } from "./driveAccounts";

test("renders the exact account action, modal, 100-link limit and disabled empty submission", () => {
 const html=renderToStaticMarkup(<DriveAccountsDialog locale="ru" draft="" batch={emptyDriveBatch} pending={false} error="" onDraft={()=>{}} onClose={()=>{}} onStart={()=>{}} onCancel={()=>{}} />);
 expect(html).toContain("Добавить TData аккаунты");expect(html).toContain('role="dialog"');expect(html).toContain("100");expect(html).toContain("disabled");
});
test("shows per-link results and cancellation without displaying input URLs", () => {
 const html=renderToStaticMarkup(<DriveAccountsDialog locale="ru" draft="" batch={{id:"batch",running:true,items:[{ordinal:1,phase:"failed",error:"drive_unavailable",added:0,skipped:0}]}} pending={false} error="" onDraft={()=>{}} onClose={()=>{}} onStart={()=>{}} onCancel={()=>{}} />);
 expect(html).toContain("Отменить импорт");expect(html).toContain("Нет доступа");expect(html).not.toContain("textarea");
});
