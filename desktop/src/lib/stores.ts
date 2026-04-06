import { writable } from "svelte/store";
import { api, type StatusResponse } from "./api";
import { licenseInfo, FREE_LICENSE } from "./license";

/** Whether we have a live connection to the DevRecall API. */
export const connected = writable(false);

/** Latest API status response. */
export const apiStatus = writable<StatusResponse | null>(null);

/** Check connection to the API and update stores. */
export async function checkConnection() {
  try {
    const status = await api.status();
    apiStatus.set(status);
    connected.set(true);
    if (status.license) {
      licenseInfo.set(status.license);
    }
  } catch {
    connected.set(false);
    apiStatus.set(null);
    licenseInfo.set(FREE_LICENSE);
  }
}
