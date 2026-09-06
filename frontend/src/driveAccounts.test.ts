import { describe, expect, test } from "vitest";
import { parseDriveInput, driveImportText } from "./driveAccounts";

describe("Drive account import input", () => {
 test("accepts 100 Google links, folders and duplicates with the exact button label", () => {
  expect(driveImportText.ru.title).toBe("Добавить TData аккаунты");
  const hundred=Array.from({length:100},(_,i)=>`https://drive.google.com/uc?id=file_${i}&export=download`).join("\n");
  expect(parseDriveInput(hundred).error).toBe("");
  expect(parseDriveInput(`${hundred}\nhttps://drive.google.com/uc?id=extra`).error).toBe("limit");
  expect(parseDriveInput("https://drive.google.com/uc?id=same\n\nhttps://drive.google.com/file/d/same/view").links).toHaveLength(1);
  expect(parseDriveInput("https://drive.google.com/drive/folders/folder").error).toBe("");
 });
 test.each(["https://evil.example/uc?id=one","https://drive.google.com.evil.example/uc?id=one","http://drive.google.com/uc?id=one","https://drive.google.com:443/uc?id=one","https://drive.google.com/uc?id=one&id=two","https://drive.google.com/uc?id=one&redirect=evil","https://drive.google.com/uc?id=one#fragment"])("rejects unsafe input %s",raw=>{
  expect(parseDriveInput(raw).error).toBe("invalid");
 });
});
