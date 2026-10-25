const fs = require('node:fs');
const path = require('node:path');
const { layout } = require('./layout.cjs');
const paths = layout();
fs.mkdirSync(path.join(paths.electron, 'node_modules'), { recursive: true });
fs.writeFileSync(path.join(paths.electron, 'node_modules/.dicam-platform.json'), JSON.stringify({ target: paths.target }));
