<template>
  <div class="landing-page space-y-5 fade-in">
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div><div class="eyebrow">LANDING NODES</div><h1 class="page-title">落地节点</h1><p class="page-subtitle">粘贴完整节点链接，面板只替换中转入口地址和端口</p></div>
      <button class="btn-primary" @click="openCreate">添加完整节点</button>
    </header>
    <div v-if="message" class="notice" :class="messageType === 'error' ? 'notice-error' : 'notice-success'">{{ message }}</div>

    <div v-if="!store.landingNodes.length" class="card empty-panel">
      <div class="empty-mark">NODE</div><h2 class="mt-4 font-semibold text-slate-800">还没有落地节点</h2>
      <p class="mt-1 text-sm text-slate-500">添加 VLESS、SS2022、VMess、Trojan 或其他受支持的完整分享链接。</p>
      <button class="btn-primary mt-5" @click="openCreate">添加第一个节点</button>
    </div>

    <div v-else class="node-grid">
      <article v-for="node in store.landingNodes" :key="node.id" class="card node-card">
        <div class="node-head">
          <div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><span class="protocol-tag">{{ (node.protocol || node.network || 'legacy').toUpperCase() }}</span><span class="state-tag" :class="node.enabled ? 'state-online' : ''">{{ node.enabled ? '启用' : '停用' }}</span></div><h2 class="mt-3 truncate text-base font-bold text-slate-800">{{ node.name }}</h2><p class="mt-1 truncate font-mono text-[11px] text-slate-400">{{ node.address }}:{{ node.port }}</p></div>
          <div class="node-actions"><button class="btn-ghost px-2 py-1 text-xs" @click="openEdit(node)">编辑</button><button class="btn-danger px-2 py-1 text-xs" @click="remove(node)">删除</button></div>
        </div>

        <div v-if="node.share_uri" class="source-panel">
          <div class="panel-label">原始节点链接</div><code class="source-uri">{{ node.share_uri }}</code>
        </div>
        <div v-else class="legacy-panel"><strong>兼容旧节点录入</strong><span>当前节点只有地址和端口，编辑时可补充完整分享链接。</span></div>

        <div class="relay-panel">
          <div class="relay-panel-head"><div><h3>中转后的节点</h3><p>仅替换 Host 和 Port，其余参数保持不变</p></div><span class="panel-code">{{ linksFor(node).length }} 个入口</span></div>
          <div v-if="linksFor(node).length" class="relay-link-list">
            <div v-for="link in linksFor(node)" :key="link.pool_id || link.service_id || `${link.host}:${link.port}:${link.service_name}`" class="relay-link-item">
              <div class="min-w-0"><strong class="block truncate text-xs text-slate-700">{{ link.service_name }}</strong><small class="mt-1 block truncate text-[10px] text-slate-400">{{ link.relay_node_name }} · {{ link.host || '未设置入口地址' }}:{{ link.port }}</small></div>
              <button v-if="link.uri" class="btn-ghost flex-none border border-slate-200 px-2 py-1 text-[10px]" @click="copyLink(link.uri)">复制</button>
              <code v-if="link.uri" class="relay-uri">{{ link.uri }}</code><span v-else class="col-span-full text-[10px] text-amber-700">{{ link.message || '暂时无法生成中转链接' }}</span>
            </div>
          </div>
          <div v-else class="relay-empty">请先创建转发服务或逻辑入口池，并将此落地节点加入目标列表。</div>
        </div>
      </article>
    </div>

    <Modal v-if="showForm" @close="showForm = false">
      <form class="space-y-5" @submit.prevent="save">
        <div><div class="eyebrow">LANDING NODE</div><h2 class="mt-1 text-lg font-bold text-slate-900">{{ editTarget ? '编辑落地节点' : '添加完整节点' }}</h2><p class="mt-2 text-xs leading-5 text-slate-500">粘贴分享链接后，保存时会自动识别协议、地址和端口。生成中转链接时只改入口 Host/Port。</p></div>
        <div><label class="field-label" for="node-share-uri">完整节点分享链接 <span v-if="!editTarget" class="text-danger">*</span></label><textarea id="node-share-uri" v-model.trim="form.share_uri" class="input node-textarea" rows="4" placeholder="vless://... 或 ss://... 或 vmess://..."></textarea><p class="field-hint">支持 VLESS（含 REALITY/WS/gRPC 参数）、SS/SS2022、VMess、Trojan、Hysteria2、TUIC。链接中的密钥和传输参数会原样保留。</p></div>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2"><div class="sm:col-span-2"><label class="field-label">节点名称 <span class="font-normal text-slate-400">（可选，留空则从链接名称推断）</span></label><input v-model.trim="form.name" class="input" placeholder="例如：香港 REALITY 主节点" /></div></div>
        <details v-if="editTarget && !form.share_uri" class="legacy-fields"><summary>兼容旧节点：手动维护地址和端口</summary><div class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2"><div><label class="field-label">IP 或域名</label><input v-model.trim="form.address" class="input" /></div><div><label class="field-label">端口</label><input v-model.number="form.port" type="number" min="1" max="65535" class="input" /></div><div><label class="field-label">网络协议</label><select v-model="form.network" class="input"><option value="tcp">TCP</option><option value="udp">UDP</option><option value="tcp+udp">TCP + UDP</option></select></div></div></details>
        <label class="flex items-center gap-2 text-sm text-slate-600"><input v-model="form.enabled" type="checkbox" />启用落地节点</label>
        <div v-if="formError" class="notice notice-error">{{ formError }}</div><div class="flex gap-3 border-t border-slate-100 pt-4"><button type="button" class="btn-ghost flex-1 border border-slate-200" @click="showForm = false">取消</button><button class="btn-primary flex-1" :disabled="saving">{{ saving ? '解析并保存中...' : '保存节点' }}</button></div>
      </form>
    </Modal>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { useRelayStore } from '../stores/relay'
import Modal from '../components/Modal.vue'

const store = useRelayStore()
const showForm = ref(false); const editTarget = ref(null); const saving = ref(false); const formError = ref(''); const message = ref(''); const messageType = ref('success')
const form = ref(blank()); const relayLinks = reactive({})
function blank() { return { name: '', share_uri: '', address: '', port: 443, network: 'tcp', protocol: '', enabled: true } }
function linksFor(node) { return relayLinks[node.id] || [] }
async function loadLinks(node) { if (!node.share_uri) { relayLinks[node.id] = []; return }; try { relayLinks[node.id] = await store.fetchLandingRelayLinks(node.id) } catch (_) { relayLinks[node.id] = [] } }
async function loadAllLinks() { await Promise.all(store.landingNodes.map(loadLinks)) }
function openCreate() { editTarget.value = null; form.value = blank(); formError.value = ''; showForm.value = true }
function openEdit(node) { editTarget.value = node; form.value = { name: node.name || '', share_uri: node.share_uri || '', address: node.address || '', port: node.port || 443, network: node.network || 'tcp', protocol: node.protocol || '', enabled: node.enabled !== false }; formError.value = ''; showForm.value = true }
async function save() { formError.value = ''; if (!form.value.share_uri && !editTarget.value) { formError.value = '请粘贴完整节点分享链接'; return }; saving.value = true; try { if (editTarget.value) await store.updateLandingNode(editTarget.value.id, form.value); else await store.createLandingNode(form.value); await store.fetchLandingNodes(); await loadAllLinks(); showForm.value = false; showMessage('节点已保存') } catch (e) { formError.value = e.response?.data?.error || '保存失败，请检查节点链接格式' } finally { saving.value = false } }
async function remove(node) { if (!confirm(`确认删除落地节点“${node.name}”？`)) return; try { await store.deleteLandingNode(node.id); delete relayLinks[node.id]; showMessage('节点已删除') } catch (e) { showMessage(e.response?.data?.error || '节点正在被转发服务使用，无法删除', 'error') } }
async function copyLink(value) { try { await navigator.clipboard.writeText(value); showMessage('中转节点链接已复制') } catch (_) { showMessage('复制失败，请手动选择链接', 'error') } }
function showMessage(text, type = 'success') { message.value = text; messageType.value = type; window.setTimeout(() => { message.value = '' }, 4500) }
onMounted(async () => { await store.fetchLandingNodes(); await loadAllLinks() })
</script>

<style scoped>
.landing-page { min-width: 0; }.eyebrow { color: #2563eb; font-size: 10px; font-weight: 800; letter-spacing: .16em; }.page-title { margin-top: 5px; color: #172033; font-size: clamp(24px, 3vw, 30px); font-weight: 750; letter-spacing: -.035em; }.page-subtitle { margin-top: 5px; color: #64748b; font-size: 12px; line-height: 1.6; }.node-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }.node-card { min-width: 0; padding: 18px; }.node-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 14px; }.node-actions { display: flex; flex: 0 0 auto; gap: 3px; }.protocol-tag,.state-tag,.panel-code { display: inline-flex; width: max-content; align-items: center; border-radius: 999px; background: #eff6ff; padding: 4px 8px; color: #2563eb; font-size: 9px; font-weight: 750; }.state-tag { background: #f1f5f9; color: #64748b; }.state-online { background: #ecfdf3; color: #15803d; }.source-panel,.legacy-panel,.relay-panel { margin-top: 16px; border-radius: 10px; }.source-panel { border: 1px solid #e5eaf1; background: #f8fafc; padding: 11px; }.panel-label { color: #94a3b8; font-size: 9px; font-weight: 750; letter-spacing: .04em; }.source-uri { display: block; max-height: 64px; margin-top: 7px; overflow: auto; color: #64748b; font-size: 10px; line-height: 1.55; white-space: pre-wrap; word-break: break-all; }.legacy-panel { display: grid; gap: 3px; border: 1px solid #fde68a; background: #fffbeb; padding: 10px 11px; color: #a16207; font-size: 10px; }.legacy-panel span { color: #b45309; }.relay-panel { border: 1px solid #dbeafe; background: #f8fbff; padding: 12px; }.relay-panel-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 10px; }.relay-panel-head h3 { color: #1e3a8a; font-size: 12px; font-weight: 750; }.relay-panel-head p { margin-top: 3px; color: #64748b; font-size: 9px; }.panel-code { background: #dbeafe; padding: 4px 7px; font-size: 8px; }.relay-link-list { display: grid; gap: 8px; margin-top: 11px; }.relay-link-item { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 5px 9px; align-items: center; border-top: 1px solid #e0ecff; padding-top: 9px; }.relay-uri { grid-column: 1 / -1; display: block; overflow: auto; border-radius: 6px; background: #fff; padding: 8px; color: #334155; font-size: 9px; line-height: 1.5; white-space: pre-wrap; word-break: break-all; }.relay-empty { margin-top: 10px; border-radius: 7px; background: #fff; padding: 10px; color: #94a3b8; font-size: 10px; line-height: 1.5; }.empty-panel { padding: 56px 24px; text-align: center; color: #64748b; }.empty-mark { display: inline-flex; align-items: center; justify-content: center; width: 48px; height: 48px; margin: 0 auto; border-radius: 11px; background: #eff6ff; color: #2563eb; font-size: 10px; font-weight: 800; letter-spacing: .08em; }.field-hint { margin-top: 6px; color: #94a3b8; font-size: 10px; line-height: 1.6; }.node-textarea { min-height: 108px; resize: vertical; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 11px; line-height: 1.55; }.legacy-fields { border: 1px solid #e5eaf1; border-radius: 9px; padding: 11px 12px; color: #64748b; font-size: 11px; }.legacy-fields summary { cursor: pointer; color: #475569; font-weight: 650; }
@media (max-width: 900px) { .node-grid { grid-template-columns: minmax(0, 1fr); } }
@media (max-width: 520px) { .node-head { flex-direction: column; }.node-actions { width: 100%; justify-content: flex-end; }.node-card { padding: 15px; } }
</style>
