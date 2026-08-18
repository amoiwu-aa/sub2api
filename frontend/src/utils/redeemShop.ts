import { sanitizeUrl } from './url'

export const REDEEM_SHOP_BUTTON_TEXT_MAX = 40
export const REDEEM_SHOP_DESCRIPTION_MAX = 500

export type RedeemShopSettings = {
  redeem_shop_enabled?: boolean
  redeem_shop_url?: string
  redeem_shop_button_text?: string
  redeem_shop_description?: string
}

export type ResolvedRedeemShop = {
  visible: boolean
  url: string
  buttonText: string
  description: string
}

function clipRunes(value: string, max: number): string {
  const chars = Array.from(value)
  return chars.length > max ? chars.slice(0, max).join('') : value
}

/** Resolve the user-facing card-shop CTA from public settings. */
export function resolveRedeemShop(
  settings?: RedeemShopSettings | Record<string, unknown> | null,
): ResolvedRedeemShop {
  const shop = (settings ?? {}) as RedeemShopSettings
  const enabled = shop.redeem_shop_enabled === true
  const url = sanitizeUrl(shop.redeem_shop_url ?? '')
  return {
    visible: enabled && url !== '',
    url,
    buttonText: clipRunes((shop.redeem_shop_button_text ?? '').trim(), REDEEM_SHOP_BUTTON_TEXT_MAX),
    description: clipRunes(
      (shop.redeem_shop_description ?? '').trim(),
      REDEEM_SHOP_DESCRIPTION_MAX,
    ),
  }
}
