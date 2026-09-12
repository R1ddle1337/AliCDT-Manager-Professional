<template>
  <Modal size="wide" @close="$emit('close')">
    <section class="billing-dialog" :aria-busy="loading">
      <header class="billing-heading">
        <h2>费用估计</h2>
        <p>{{ account.name }}<span v-if="billing?.bill?.billing_cycle"> · {{ billing.bill.billing_cycle }}</span></p>
      </header>

      <div v-if="error" class="notice notice-error" role="alert">{{ error }}<span v-if="billing">（以下保留上次查询结果）</span></div>
      <div v-if="billing?.errors?.length" class="notice notice-error" role="alert">{{ billing.errors.join('；') }}</div>
      <div v-if="billing?.disabled" class="notice notice-info">该账户暂不支持账单查询。</div>
      <template v-else>
        <dl class="billing-metrics">
          <div><dt>账户余额</dt><dd>{{ money(billing?.balance?.available_amount, billing?.balance?.currency) }}</dd></div>
          <div><dt>本月已出账（税前）</dt><dd>{{ money(total, billing?.bill?.currency) }}</dd></div>
          <div><dt>本月待还</dt><dd>{{ money(billing?.bill?.total_outstanding, billing?.bill?.currency) }}</dd></div>
          <div class="billing-estimate"><dt>本月费用估计（税前）</dt><dd>{{ estimate === null ? (billing?.bill ? '待积累' : '—') : money(estimate, billing?.bill?.currency) }}</dd></div>
        </dl>
        <p class="billing-note">按本月已出账费用日均折算，实际以阿里云账单为准。</p>
        <details v-if="billing?.bill?.details?.length" class="billing-details">
          <summary>费用明细</summary>
          <div v-for="(detail, index) in billing.bill.details" :key="index" class="billing-detail">
            <span>{{ detail.product || '其他产品' }}</span>
            <strong>{{ money(detail.pretax_amount, billing.bill.currency) }}</strong>
          </div>
        </details>
      </template>

      <footer class="billing-actions">
        <span role="status">{{ loading ? '查询阿里云账单中…' : (updatedAt ? `查询于 ${formatDateTime(updatedAt)}` : '尚未获取账单') }}</span>
        <button type="button" class="btn-ghost border border-slate-200" :disabled="loading" @click="refresh">{{ loading ? '查询中…' : '刷新费用' }}</button>
      </footer>
    </section>
  </Modal>
</template>

<script setup>
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { useStore } from '../stores'
import { billTotal, estimateMonthlyCost, formatMoney } from '../utils/billing'
import { formatDateTime } from '../utils/time'
import { apiErrorMessage } from '../utils/session'
import Modal from './Modal.vue'

const props = defineProps({ account: { type: Object, required: true } })
defineEmits(['close'])
const store = useStore()
const billing = ref(null)
const loading = ref(false)
const error = ref('')
const updatedAt = ref(null)
const abortController = new AbortController()
const total = computed(() => billTotal(billing.value?.bill))
const estimate = computed(() => estimateMonthlyCost(billing.value?.bill, updatedAt.value || new Date()))
const money = (value, currency) => formatMoney(value, currency || 'USD')

async function refresh() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const response = await store.getBilling(props.account.id, abortController.signal)
    if (abortController.signal.aborted) return
    billing.value = response
    updatedAt.value = new Date()
  } catch (err) {
    if (!abortController.signal.aborted) error.value = apiErrorMessage(err, '费用查询失败，请重试')
  } finally {
    loading.value = false
  }
}

onMounted(refresh)
onBeforeUnmount(() => abortController.abort())
</script>

<style scoped>
.billing-dialog { display: grid; min-width: 0; gap: 18px; }
.billing-heading { min-width: 0; padding-right: 32px; }
.billing-heading h2 { color: #1e293b; font-size: 20px; font-weight: 650; }
.billing-heading p { margin-top: 5px; color: #64748b; font-size: 13px; overflow-wrap: anywhere; }
.billing-metrics { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.billing-metrics > div { min-width: 0; border: 1px solid #e7e9ee; border-radius: 8px; padding: 14px; }
.billing-metrics dt { color: #64748b; font-size: 12px; }
.billing-metrics dd { margin-top: 8px; color: #1f2937; font-size: clamp(17px, 3vw, 22px); font-weight: 600; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.billing-metrics .billing-estimate { background: #f8fafc; }
.billing-note { color: #64748b; font-size: 12px; line-height: 1.6; }
.billing-details summary { cursor: pointer; color: #475569; font-size: 13px; }
.billing-detail { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 8px 16px; margin-top: 10px; border-top: 1px solid #f1f5f9; padding-top: 10px; font-size: 13px; }
.billing-detail span { min-width: 0; color: #64748b; overflow-wrap: anywhere; }
.billing-detail strong { color: #334155; font-variant-numeric: tabular-nums; }
.billing-actions { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 10px; border-top: 1px solid #e7e9ee; padding-top: 14px; }
.billing-actions span { color: #64748b; font-size: 12px; }
@media (max-width: 380px) { .billing-metrics { grid-template-columns: minmax(0, 1fr); } }
</style>
