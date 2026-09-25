// Thông tin hiển thị của từng loại kênh chat, dùng chung cho mọi trang để
// thêm kênh mới chỉ phải khai báo một chỗ.
export interface ChannelTypeInfo {
  value: string
  label: string
  short: string
  icon: string
  color: string
}

export const CHANNEL_TYPES: ChannelTypeInfo[] = [
  { value: 'zalo_oa', label: 'Zalo OA', short: 'Zalo', icon: 'mdi-chat', color: 'green' },
  { value: 'facebook', label: 'Facebook Fanpage', short: 'FB', icon: 'mdi-facebook-messenger', color: 'blue' },
  { value: 'pancake', label: 'Pancake', short: 'Pancake', icon: 'mdi-forum', color: 'orange' },
  { value: 'webhook', label: 'Webhook / CRM', short: 'Webhook', icon: 'mdi-webhook', color: 'teal' },
]

export function channelTypeInfo(type: string): ChannelTypeInfo {
  return CHANNEL_TYPES.find(c => c.value === type)
    || { value: type, label: type, short: type, icon: 'mdi-chat', color: 'grey' }
}
