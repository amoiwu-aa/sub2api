import { describe, expect, it } from 'vitest'

import { resolveRedeemShop } from '../redeemShop'

describe('resolveRedeemShop', () => {
  it('hides the CTA when the shop is disabled or the URL is invalid', () => {
    expect(resolveRedeemShop(null).visible).toBe(false)
    expect(
      resolveRedeemShop({
        redeem_shop_enabled: false,
        redeem_shop_url: 'https://shop.example.com/buy',
      }).visible,
    ).toBe(false)
    expect(
      resolveRedeemShop({
        redeem_shop_enabled: true,
        redeem_shop_url: '/relative',
      }).visible,
    ).toBe(false)
    expect(
      resolveRedeemShop({
        redeem_shop_enabled: true,
        redeem_shop_url: 'javascript:alert(1)',
      }).visible,
    ).toBe(false)
  })

  it('shows a sanitized absolute URL and trims optional copy', () => {
    const shop = resolveRedeemShop({
      redeem_shop_enabled: true,
      redeem_shop_url: 'https://shop.example.com/buy?sku=1',
      redeem_shop_button_text: '  去卡网购买  ',
      redeem_shop_description: '  先买后兑  ',
    })

    expect(shop.visible).toBe(true)
    expect(shop.url).toBe('https://shop.example.com/buy?sku=1')
    expect(shop.buttonText).toBe('去卡网购买')
    expect(shop.description).toBe('先买后兑')
  })
})
