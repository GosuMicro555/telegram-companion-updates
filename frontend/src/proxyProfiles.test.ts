import { describe, expect, it } from "vitest";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ProxyProfilesPanel } from "./ProxyProfilesPanel";
import { emptyProxyDraft, proxyEndpointRequired, proxyRouteUnavailable, toProxyProfile } from "./proxyProfiles";
import { proxyRouteLabel, proxyStatusErrorLabel, proxyTransportLabel } from "./proxyStatus";
import { t } from "./i18n";

describe("proxy profile view model", () => {
  it("requires an endpoint only for a new profile", () => {
    expect(proxyEndpointRequired(emptyProxyDraft())).toBe(true);
    expect(proxyEndpointRequired({ ...emptyProxyDraft(), id: "existing" })).toBe(false);
  });

  it("normalizes safe backend fields without credentials", () => {
    expect(toProxyProfile({ id: "route", name: "Office", protocol: "http", endpoint: "10.0.0.1:8080", state: "ready", usage: 4, capacity: 10, passwordConfigured: true })).toEqual({
      id: "route", name: "Office", protocol: "http", endpoint: "10.0.0.1:8080", state: "ready", usage: 4, capacity: 10,
      lastError: "", enabled: true, passwordConfigured: true
    });
  });

  it("disables unavailable and full routes except the current assignment", () => {
    const full = toProxyProfile({ id: "full", state: "full", usage: 10, capacity: 10 });
    expect(proxyRouteUnavailable(full, "other")).toBe(true);
    expect(proxyRouteUnavailable(full, "full")).toBe(false);
    expect(proxyRouteUnavailable(toProxyProfile({ id: "ready", state: "ready", usage: 1 }), "other")).toBe(false);
  });

  it("preserves zero capacity and keeps an unlimited route assignable", () => {
    const system = toProxyProfile({ id: "system", state: "ready", usage: 500, capacity: 0 });
    expect(system.capacity).toBe(0);
    expect(proxyRouteUnavailable(system, "other")).toBe(false);
  });

  it("keeps an unavailable custom route disabled at its finite capacity", () => {
    const custom = toProxyProfile({ id: "custom", state: "unavailable", usage: 0, capacity: 10 });
    expect(custom.capacity).toBe(10);
    expect(proxyRouteUnavailable(custom, "system")).toBe(true);
  });

  it("localizes managed proxy failures without reflecting raw bridge material", () => {
    expect(proxyStatusErrorLabel("ru", "managed_proxy_unavailable")).toBe(t("ru", "proxyManagedUnavailable"));
    expect(proxyStatusErrorLabel("en", "bridge_config_invalid")).toBe(t("en", "proxyBridgeConfigInvalid"));
    expect(proxyStatusErrorLabel("ru", "all_candidates_exhausted")).toBe(t("ru", "proxyAllCandidatesExhausted"));
    expect(proxyStatusErrorLabel("ru", "bridge_config_invalid cert=private-material endpoint=198.51.100.9")).toBe("");
  });

  it("localizes the managed route without changing the stable route id", () => {
    expect(proxyRouteLabel("ru", "system", "Tor/Snowflake")).toBe(t("ru", "managedProxy"));
    expect(proxyRouteLabel("en", "system", "Tor/Snowflake")).toBe(t("en", "managedProxy"));
    expect(proxyRouteLabel("ru", "custom", "Tor/Snowflake")).toBe("Tor/Snowflake");
  });

  it("exposes only safe localized transport labels", () => {
    expect(proxyTransportLabel("ru", "obfs4")).toBe(t("ru", "proxyTransportObfs4"));
    expect(proxyTransportLabel("en", "snowflake")).toBe(t("en", "proxyTransportSnowflake"));
    expect(proxyTransportLabel("ru", "bridge cert=secret endpoint=203.0.113.10")).toBe(t("ru", "proxyTransportUnknown"));
  });

  it("renders the localized managed route name in the profile panel", () => {
    const managedName = proxyRouteLabel("ru", "system", "Tor/Snowflake");
    const html = renderToStaticMarkup(createElement(ProxyProfilesPanel, {
      rows: [toProxyProfile({ id: "system", name: managedName, state: "ready", usage: 1, capacity: 0 })],
      labels: {
        title: "Routes", add: "Add", route: "Route", endpoint: "Endpoint", state: "State", capacity: "Capacity", actions: "Actions",
        edit: "Edit", remove: "Remove", check: "Check", save: "Save", cancel: "Cancel", name: "Name", protocol: "Protocol",
        host: "Host", port: "Port", username: "Username", password: "Password", enabled: "Enabled", passwordConfigured: "Password configured", clearPassword: "Clear password"
      },
      busyID: "",
      onSave: async () => {},
      onDelete: async () => {},
      onCheck: async () => {}
    }));

    expect(html).toContain(managedName);
    expect(html).not.toContain("Tor/Snowflake");
  });

  it("renders infinity for the system route and ten for custom routes", () => {
    const html = renderToStaticMarkup(createElement(ProxyProfilesPanel, {
      rows: [
        toProxyProfile({ id: "system", name: "Tor/Snowflake", state: "ready", usage: 500, capacity: 0 }),
        toProxyProfile({ id: "custom", name: "Custom", state: "ready", usage: 4, capacity: 10 })
      ],
      labels: {
        title: "Routes", add: "Add", route: "Route", endpoint: "Endpoint", state: "State", capacity: "Capacity", actions: "Actions",
        edit: "Edit", remove: "Remove", check: "Check", save: "Save", cancel: "Cancel", name: "Name", protocol: "Protocol",
        host: "Host", port: "Port", username: "Username", password: "Password", enabled: "Enabled", passwordConfigured: "Password configured", clearPassword: "Clear password"
      },
      busyID: "",
      onSave: async () => {},
      onDelete: async () => {},
      onCheck: async () => {}
    }));

    expect(html).toContain("500 / ∞");
    expect(html).toContain("4 / 10");
  });
});
