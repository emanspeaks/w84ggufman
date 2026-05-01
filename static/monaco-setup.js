// Bootstrap Monaco Editor.
// Sets MonacoEnvironment, dynamically loads the CDN loader, then configures
// require and exposes window.monacoReady for the rest of the app.

self.MonacoEnvironment = {
  getWorkerUrl: function () {
    return 'data:text/javascript;charset=utf-8,' + encodeURIComponent(
      "self.MonacoEnvironment = { baseUrl: 'https://cdn.jsdelivr.net/npm/monaco-editor@0.52.0/min/' };" +
      "importScripts('https://cdn.jsdelivr.net/npm/monaco-editor@0.52.0/min/vs/base/worker/workerMain.js');"
    );
  },
};

window.monacoReady = new Promise(function (resolve) {
  var script = document.createElement('script');
  script.src = 'https://cdn.jsdelivr.net/npm/monaco-editor@0.52.0/min/vs/loader.js';
  script.onload = function () {
    require.config({ paths: { vs: 'https://cdn.jsdelivr.net/npm/monaco-editor@0.52.0/min/vs' } });
    require(['vs/editor/editor.main'], function () { resolve(window.monaco); });
  };
  document.head.appendChild(script);
});
