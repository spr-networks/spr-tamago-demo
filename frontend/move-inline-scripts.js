const fs = require('fs')
const path = require('path')

const output = path.join(__dirname, 'build', 'index.html')
const scripts = []
const withoutScripts = fs.readFileSync(output, 'utf8').replace(
  /<script>[^]*?<\/script>/g,
  (script) => {
    scripts.push(script)
    return ''
  }
)

if (scripts.length !== 1 || !withoutScripts.includes('<div id="root"></div>')) {
  throw new Error('unexpected single-file frontend layout')
}

const html = withoutScripts.replace('</body>', () => `${scripts[0]}</body>`)
fs.writeFileSync(output, html)
