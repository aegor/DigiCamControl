const { contextBridge, ipcRenderer } = require('electron');
contextBridge.exposeInMainWorld('dicam', {
  openNetworkSettings: () => ipcRenderer.invoke('open-network-settings'),
  settings: () => ipcRenderer.invoke('settings'),
  connect: config => ipcRenderer.invoke('connect', config),
  status: () => ipcRenderer.invoke('status'),
  command: (name, duration) => ipcRenderer.invoke('command', name, duration),
  chooseDirectory: () => ipcRenderer.invoke('choose-directory'),
  openRecordings: () => ipcRenderer.invoke('open-recordings')
});
