import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, relative, resolve } from 'node:path'
import fg from 'fast-glob'

/**
 * 护栏：禁止在 <style scoped> 里把 :global(...) 用作前导 + 后代选择器。
 *
 * 本项目的 Vue scoped-CSS 编译会把 `:global(X) Y` 这种规则**整条丢弃**——
 * 编译产物里一条不剩，浏览器端 el.matches() 甚至返回 true，因为选择器本身
 * 没问题，只是规则压根没被生成。症状永远是「深/浅色主题的样式没生效」，
 * 而且极难从现象追到成因。
 *
 * 已经踩过至少四次：
 *   - SettingsView 的 tab（前人踩到，改用无作用域块绕过并留了注释）
 *   - LoginView 的整套浅色表单样式（11 条规则全是死的）
 *   - StatCard / CostTriplet / ChartEmptyState / AppHeader 的深色样式
 *   - GalaxyBackground、KeyUsageView（本次扫描发现）
 *
 * 可用的替代写法：
 *   1. CSS 自定义属性：在 style.css 的 :root / .dark 上声明，组件 var() 消费（首选）；
 *   2. Tailwind 的 dark: 变体（编译成 :is(.dark *)，不受此影响）；
 *   3. 实在需要写选择器时，放在同文件的**无作用域** <style> 块里。
 */

const projectRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..')

/** 匹配 :global(...) 后面还跟着别的选择器的情况。 */
const LEADING_GLOBAL_DESCENDANT = /:global\([^)]*\)\s*[^\s{,][^{,]*[{,]/

function scopedStyleBlocks(source: string): string[] {
  const blocks: string[] = []
  const re = /<style([^>]*)>([\s\S]*?)<\/style>/g
  let m: RegExpExecArray | null
  while ((m = re.exec(source))) {
    if (/\bscoped\b/.test(m[1])) blocks.push(m[2])
  }
  return blocks
}

describe('scoped CSS 不得使用 :global(...) 前导后代选择器', () => {
  it('全部 .vue 文件均无该写法', async () => {
    const files = await fg('src/**/*.vue', { cwd: projectRoot, absolute: true })
    expect(files.length).toBeGreaterThan(0)

    const offenders: string[] = []
    for (const file of files) {
      const source = readFileSync(file, 'utf8')
      for (const block of scopedStyleBlocks(source)) {
        // 逐行看，便于在报错里指出具体那一行。
        block.split('\n').forEach((line, i) => {
          const trimmed = line.trim()
          if (trimmed.startsWith('*') || trimmed.startsWith('//')) return
          if (LEADING_GLOBAL_DESCENDANT.test(trimmed)) {
            offenders.push(`${relative(projectRoot, file)}: ${trimmed}`)
          }
        })
      }
    }

    expect(
      offenders,
      '这些规则会被 Vue 的 scoped-CSS 编译整条丢弃（详见本文件顶部注释）。\n' +
        '改用 CSS 变量、Tailwind dark: 变体，或放进无作用域 <style> 块：\n' +
        offenders.join('\n')
    ).toEqual([])
  })
})
