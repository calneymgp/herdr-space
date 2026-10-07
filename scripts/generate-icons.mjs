// Render the vector app mark into the install icons. No production dependencies.
import {readFile} from 'node:fs/promises'
import {fileURLToPath} from 'node:url'
import path from 'node:path'
import {chromium} from '../.tools/browser/node_modules/playwright/index.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const icons = path.join(root, 'web/public/icons')
const svg = await readFile(path.join(icons, 'icon.svg'), 'utf8')
const browser = await chromium.launch({executablePath:process.env.HERDR_CHROME_EXECUTABLE || undefined,headless:true,args:['--no-sandbox']})
try {
  for (const [size, name] of [[192, 'icon-192.png'], [512, 'icon-512.png'], [180, 'apple-touch-icon.png']]) {
    const page = await browser.newPage({viewport:{width:size,height:size},deviceScaleFactor:1})
    await page.setContent(`<style>html,body{margin:0;width:100%;height:100%;background:#18181b}svg{display:block;width:100%;height:100%}</style>${svg}`)
    await page.screenshot({path:path.join(icons,name),omitBackground:false})
    await page.close()
  }
} finally {
  await browser.close()
}
console.log('Generated install icons from web/public/icons/icon.svg')
