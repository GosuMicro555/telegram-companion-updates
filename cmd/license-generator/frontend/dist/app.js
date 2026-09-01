const elements = {
  machineID: document.getElementById("machine-id"),
  owner: document.getElementById("owner"),
  comment: document.getElementById("comment"),
  expiryEnabled: document.getElementById("expiry-enabled"),
  expiry: document.getElementById("expiry"),
  issue: document.getElementById("issue"),
  token: document.getElementById("token"),
  licenseID: document.getElementById("license-id"),
  copy: document.getElementById("copy"),
  save: document.getElementById("save"),
  password: document.getElementById("backup-password"),
  backup: document.getElementById("backup"),
  restore: document.getElementById("restore"),
  backupDot: document.getElementById("backup-dot"),
  backupState: document.getElementById("backup-state"),
  publicKey: document.getElementById("public-key"),
  seedID: document.getElementById("seed-id"),
  exportBootstrapSeed: document.getElementById("export-bootstrap-seed"),
  revocationPublicKey: document.getElementById("revocation-public-key"),
  revocationPublicationState: document.getElementById("revocation-publication-state"),
  revocationCredential: document.getElementById("revocation-credential"),
  configureRevocation: document.getElementById("configure-revocation"),
  importLicense: document.getElementById("import-license"),
  historyBody: document.getElementById("history-body"),
  historyCount: document.getElementById("history-count"),
  toast: document.getElementById("toast"),
};

let issued = null;
let toastTimer = null;
let currentRevocationPublicationCode = "unavailable";

function backend() {
  return window.go?.main?.GeneratorUI;
}

function notify(message, error = false) {
  clearTimeout(toastTimer);
  elements.toast.textContent = message;
  elements.toast.className = `toast visible${error ? " error" : ""}`;
  toastTimer = setTimeout(() => { elements.toast.className = "toast"; }, 3200);
}

function setBusy(button, busy, label) {
  button.disabled = busy;
  if (!button.dataset.label) button.dataset.label = button.textContent;
  button.textContent = busy ? label : button.dataset.label;
}

function formatDate(value) {
  if (!value) return "Бессрочно";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString("ru-RU");
}

function revocationLabel(state) {
  if (state === "revoked") return "Отозвана";
  if (state === "pending") return "Публикация отзыва";
  if (state === "failed") return "Ошибка публикации";
  return "Активна";
}

function revocationPublicationLabel(code) {
  const labels = {
    credential_required: "Требуется доступ к публикации",
    ready: "Публикация готова",
    restore_required: "Требуется восстановить V3 backup",
    remote_restore_required: "Требуется восстановить V3 backup",
    restart_required: "Ключ восстановлен; перезапустите генератор",
    unavailable: "Публикация временно недоступна",
  };
  return labels[code] || labels.unavailable;
}

function confirmRevocationAction(entry, retry) {
  const action = retry ? "Повторить публикацию отзыва?" : "Опубликовать отзыв лицензии?";
  return window.confirm([
    action,
    `Владелец: ${entry.owner}`,
    `Срок: ${formatDate(entry.expiresAt)}`,
    `License ID: ${entry.licenseID}`,
    "После успешной публикации отзыв действует необратимо.",
    "Чтобы снова предоставить доступ, потребуется выпустить новую лицензию.",
  ].join("\n"));
}

function renderLicenses(licenses) {
  elements.historyCount.textContent = String(licenses.length);
  elements.historyBody.replaceChildren();
  if (licenses.length === 0) {
    const row = document.createElement("tr");
    const cell = document.createElement("td");
    cell.colSpan = 6;
    cell.className = "empty";
    cell.textContent = "Лицензии ещё не выпускались";
    row.appendChild(cell);
    elements.historyBody.appendChild(row);
    return;
  }
  [...licenses].reverse().forEach((entry) => {
    const row = document.createElement("tr");
    [formatDate(entry.issuedAt), entry.owner, entry.licenseID, formatDate(entry.expiresAt), revocationLabel(entry.revocationState)].forEach((value) => {
      const cell = document.createElement("td");
      cell.textContent = value;
      row.appendChild(cell);
    });
    const actionCell = document.createElement("td");
    if (entry.revocationState !== "revoked") {
      const action = document.createElement("button");
      const retry = entry.revocationState === "pending" || entry.revocationState === "failed";
      action.type = "button";
      action.className = "secondary-button table-action";
      action.textContent = retry ? "Повторить" : "Отозвать лицензию";
      action.disabled = currentRevocationPublicationCode !== "ready";
      action.addEventListener("click", async () => {
        if (!confirmRevocationAction(entry, retry)) return;
        action.disabled = true;
        try {
          if (retry) await backend().RetryRevocation(entry.licenseID);
          else await backend().RevokeLicense(entry.licenseID);
          notify(retry ? "Статус публикации обновлён" : "Отзыв опубликован");
        } catch (_) {
          notify("Ошибка публикации. Статус будет обновлён; безопасно повторите операцию при необходимости.", true);
        } finally {
          await refreshStatus().catch(() => {
            notify("Не удалось обновить статус публикации", true);
          });
          action.disabled = currentRevocationPublicationCode !== "ready";
        }
      });
      actionCell.appendChild(action);
    } else {
      actionCell.textContent = "—";
    }
    row.appendChild(actionCell);
    elements.historyBody.appendChild(row);
  });
}

async function refreshStatus() {
  const api = backend();
  if (!api) {
    notify("Backend генератора недоступен", true);
    return;
  }
  const status = await api.GetStatus();
  elements.publicKey.textContent = status.licensePublicKey || "Недоступен";
  elements.revocationPublicKey.textContent = status.revocationPublicKey || "Требуется восстановление или provisioning";
  currentRevocationPublicationCode = status.revocationPublicationCode || "unavailable";
  elements.revocationPublicationState.textContent = revocationPublicationLabel(currentRevocationPublicationCode);
  elements.seedID.textContent = status.seedID || "Недоступен";
  elements.backupDot.classList.toggle("ready", Boolean(status.backupConfirmed));
  elements.backupState.textContent = status.backupConfirmed ? "Backup ключа подтверждён" : "Backup ключа ещё не создан";
  renderLicenses(status.licenses || []);
}

elements.machineID.addEventListener("input", () => {
  elements.machineID.value = elements.machineID.value.toUpperCase().replace(/[^A-F0-9]/g, "").slice(0, 64);
});

elements.expiryEnabled.addEventListener("change", () => {
  elements.expiry.disabled = !elements.expiryEnabled.checked;
  if (!elements.expiryEnabled.checked) elements.expiry.value = "";
});

elements.issue.addEventListener("click", async () => {
  const api = backend();
  if (!api) return;
  setBusy(elements.issue, true, "Выпуск...");
  try {
    const expiresAt = elements.expiryEnabled.checked && elements.expiry.value
      ? new Date(`${elements.expiry.value}T23:59:59Z`).toISOString()
      : "";
    issued = await api.Issue({
      machineID: elements.machineID.value,
      owner: elements.owner.value,
      comment: elements.comment.value,
      expiresAt,
    });
    elements.token.value = issued.token;
    elements.licenseID.textContent = issued.payload.license_id;
    elements.copy.disabled = false;
    elements.save.disabled = false;
    await refreshStatus();
    notify("Лицензия выпущена и записана в историю");
  } catch (error) {
    notify(`Не удалось выпустить лицензию: ${String(error)}`, true);
  } finally {
    setBusy(elements.issue, false, "");
  }
});

elements.copy.addEventListener("click", async () => {
  if (!issued) return;
  try {
    await backend().CopyLicense(issued.token);
    notify("Ключ скопирован");
  } catch (error) {
    notify(`Не удалось скопировать ключ: ${String(error)}`, true);
  }
});

elements.save.addEventListener("click", async () => {
  if (!issued) return;
  try {
    const saved = await backend().SaveLicense(issued.token, issued.payload.license_id);
    if (saved) notify("Файл лицензии сохранён");
  } catch (error) {
    notify(`Не удалось сохранить лицензию: ${String(error)}`, true);
  }
});

elements.backup.addEventListener("click", async () => {
  setBusy(elements.backup, true, "Создание...");
  try {
    const saved = await backend().ExportBackup(elements.password.value);
    if (saved) {
      elements.password.value = "";
      await refreshStatus();
      notify("Зашифрованный backup сохранён");
    }
  } catch (error) {
    notify(`Не удалось создать backup: ${String(error)}`, true);
  } finally {
    setBusy(elements.backup, false, "");
  }
});

elements.exportBootstrapSeed.addEventListener("click", async () => {
  setBusy(elements.exportBootstrapSeed, true, "Шифрование...");
  try {
    const saved = await backend().ExportBootstrapSeed(elements.password.value);
    if (saved) notify("Зашифрованный seed для сборки сохранён");
  } catch (error) {
    notify(`Не удалось экспортировать seed для сборки: ${String(error)}`, true);
  } finally {
    setBusy(elements.exportBootstrapSeed, false, "");
  }
});

elements.restore.addEventListener("click", async () => {
  setBusy(elements.restore, true, "Восстановление...");
  try {
    const restored = await backend().RestoreBackup(elements.password.value);
    if (restored) {
      elements.password.value = "";
      issued = null;
      elements.token.value = "";
      elements.licenseID.textContent = "";
      elements.copy.disabled = true;
      elements.save.disabled = true;
      await refreshStatus();
      notify("Активный ключ восстановлен");
    }
  } catch (error) {
    notify(`Не удалось восстановить ключ: ${String(error)}`, true);
  } finally {
    setBusy(elements.restore, false, "");
  }
});

elements.importLicense.addEventListener("click", async () => {
  setBusy(elements.importLicense, true, "Импорт...");
  try {
    const imported = await backend().ImportLicense();
    if (imported?.licenseID) {
      await refreshStatus();
      notify("Лицензия добавлена в локальный реестр");
    }
  } catch (_) {
    notify("Не удалось импортировать лицензию", true);
  } finally {
    setBusy(elements.importLicense, false, "");
  }
});

elements.configureRevocation.addEventListener("click", async () => {
  const credential = elements.revocationCredential.value;
  elements.revocationCredential.value = "";
  setBusy(elements.configureRevocation, true, "Сохранение...");
  try {
    await backend().ConfigureRevocationCredential(credential);
    await refreshStatus();
    notify("Учётные данные публикации сохранены в защищённом хранилище");
  } catch (_) {
    notify("Не удалось сохранить учётные данные публикации", true);
  } finally {
    setBusy(elements.configureRevocation, false, "");
  }
});

window.addEventListener("DOMContentLoaded", () => { void refreshStatus(); });
