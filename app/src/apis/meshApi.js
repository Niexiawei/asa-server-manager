import apiClient from '@/utils/http.js'

// 管理器互控（docs/REMOTE_MANAGER_MESH_PLAN.md §12 P3-8）。/api/mesh/* 永远打本机（utils/peerContext.js 不改写它）。
// 响应是 {success, message, data}，这里统一取出 data。

const data = p => p.then(res => res?.data)

/** 本机状态：节点 ID、协调节点连接、Peer 端口、候选地址、版本、control_role… */
export const getMeshStatus = () => data(apiClient.get('/api/mesh/status'))

/** 改配置（只传要改的字段）：label / peer_port / no_listen / public_addrs / control_role。保存后热应用 */
export const updateMeshConfig = (patch) => data(apiClient.put('/api/mesh/config', patch))

export const joinCoordinator = (blob) => data(apiClient.post('/api/mesh/join', {blob}))
export const leaveCoordinator = () => data(apiClient.post('/api/mesh/leave'))
export const enableMesh = () => data(apiClient.post('/api/mesh/enable'))
export const disableMesh = () => data(apiClient.post('/api/mesh/disable'))

/** 对端列表（含运行时状态：路径、最近一次 Hello） */
export const listPeers = () => data(apiClient.get('/api/mesh/peers'))
export const helloPeer = (id) => data(apiClient.post(`/api/mesh/peers/${encodeURIComponent(id)}/hello`))
/** 只改出现的字段：label / granted_role（"" = 撤销）/ addrs */
export const updatePeer = (id, patch) => data(apiClient.put(`/api/mesh/peers/${encodeURIComponent(id)}`, patch))
export const forgetPeer = (id) => data(apiClient.delete(`/api/mesh/peers/${encodeURIComponent(id)}`))

/** 配对：{invite} 或 {node_id, addrs} */
export const pairPeer = (body) => data(apiClient.post('/api/mesh/pair', body))

export const listInvites = () => data(apiClient.get('/api/mesh/invites'))
/** {role, ttl_seconds, with_local_addrs, addrs, note} → {invite, record}；整串只返回这一次 */
export const createInvite = (body) => data(apiClient.post('/api/mesh/invites', body))
export const deleteInvite = (id) => data(apiClient.delete(`/api/mesh/invites/${encodeURIComponent(id)}`))

export const listRequests = () => data(apiClient.get('/api/mesh/requests'))
export const approveRequest = (id, role) => data(apiClient.post(`/api/mesh/requests/${encodeURIComponent(id)}`, {role}))
export const rejectRequest = (id) => data(apiClient.delete(`/api/mesh/requests/${encodeURIComponent(id)}`))
