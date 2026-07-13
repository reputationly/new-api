// Semi Chat 根节点监听 dragOver 后会弹出整块「拖拽文件上传」遮罩,当前版本(2.72.2)
// 没有 prop 可关闭。图片/视频/音频体验区的文件上传统一在左侧配置面板的拖拽框里,
// 聊天区不接收文件:在捕获阶段拦截 drag 事件——遮罩不再弹出,拖到聊天区时光标
// 显示「禁止投放」,引导用户拖到配置面板的上传区。
// 用法:<div onDragOverCapture={blockChatDrag} onDropCapture={blockChatDrag}><Chat/></div>
export const blockChatDrag = (e) => {
  e.preventDefault();
  e.stopPropagation();
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'none';
};
