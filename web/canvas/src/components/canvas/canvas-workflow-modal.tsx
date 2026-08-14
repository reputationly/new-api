// 工作流面板:保存选中子图 / 插入已有工作流。
// 插入前如果工作流带变量,先让用户填一遍——这是它区别于「复制粘贴节点」的地方。

import { useEffect, useMemo, useState } from "react";
import { Button, Empty, Input, Modal, Popconfirm, Tabs, Tooltip, message } from "antd";
import { Download, Trash2, Upload } from "lucide-react";

import { canvasThemes } from "@/lib/canvas-theme";
import { useThemeStore } from "@/stores/use-theme-store";
import { useWorkflowStore } from "@/stores/use-workflow-store";
import type { CanvasWorkflow } from "@/lib/canvas/canvas-workflow";

export function CanvasWorkflowModal({
    open,
    selectedCount,
    onClose,
    onSave,
    onInsert,
}: {
    open: boolean;
    selectedCount: number;
    onClose: () => void;
    onSave: (title: string, description: string) => void;
    onInsert: (workflow: CanvasWorkflow, values: Record<string, string>) => void;
}) {
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    const { workflows, removeWorkflow, saveWorkflow } = useWorkflowStore();
    const [title, setTitle] = useState("");
    const [description, setDescription] = useState("");
    const [pending, setPending] = useState<CanvasWorkflow | null>(null);
    const [values, setValues] = useState<Record<string, string>>({});

    useEffect(() => {
        if (!open) {
            setPending(null);
            setValues({});
        }
    }, [open]);

    const sorted = useMemo(() => [...workflows].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)), [workflows]);

    const confirmInsert = () => {
        if (!pending) return;
        onInsert(pending, values);
        setPending(null);
        setValues({});
        onClose();
    };

    const exportOne = (workflow: CanvasWorkflow) => {
        const blob = new Blob([JSON.stringify(workflow, null, 2)], { type: "application/json" });
        const url = URL.createObjectURL(blob);
        const anchor = document.createElement("a");
        anchor.href = url;
        anchor.download = `${workflow.title || "workflow"}.json`;
        anchor.click();
        URL.revokeObjectURL(url);
    };

    const importFile = () => {
        const input = document.createElement("input");
        input.type = "file";
        input.accept = "application/json";
        input.onchange = async () => {
            const file = input.files?.[0];
            if (!file) return;
            try {
                const parsed = JSON.parse(await file.text()) as CanvasWorkflow;
                if (!Array.isArray(parsed?.nodes) || !parsed.nodes.length) throw new Error("文件里没有节点");
                saveWorkflow({ ...parsed, updatedAt: new Date().toISOString() });
                message.success(`已导入「${parsed.title}」`);
            } catch (error) {
                message.error(error instanceof Error ? `导入失败：${error.message}` : "导入失败");
            }
        };
        input.click();
    };

    return (
        <Modal title="工作流" open={open} onCancel={onClose} footer={null} width={640} destroyOnHidden>
            <Tabs
                items={[
                    {
                        key: "insert",
                        label: `插入（${workflows.length}）`,
                        children: pending ? (
                            <div className="space-y-3 pt-2">
                                <div className="text-sm font-medium">{pending.title}</div>
                                {pending.variables.length ? (
                                    <>
                                        <div className="text-xs" style={{ color: theme.node.muted }}>
                                            填写变量后插入，留空则保留占位符 <code>{"{{名字}}"}</code>
                                        </div>
                                        {pending.variables.map((name) => (
                                            <div key={name} className="flex items-center gap-2">
                                                <span className="w-24 shrink-0 truncate text-sm" title={name}>
                                                    {name}
                                                </span>
                                                <Input value={values[name] || ""} placeholder={`替换 {{${name}}}`} onChange={(event) => setValues((prev) => ({ ...prev, [name]: event.target.value }))} />
                                            </div>
                                        ))}
                                    </>
                                ) : (
                                    <div className="text-xs" style={{ color: theme.node.muted }}>
                                        这个工作流没有变量，直接插入即可。
                                    </div>
                                )}
                                <div className="flex justify-end gap-2 pt-1">
                                    <Button onClick={() => setPending(null)}>返回</Button>
                                    <Button type="primary" onClick={confirmInsert}>
                                        插入画布
                                    </Button>
                                </div>
                            </div>
                        ) : sorted.length ? (
                            <div className="thin-scrollbar max-h-[420px] space-y-2 overflow-y-auto pt-2">
                                {sorted.map((workflow) => (
                                    <div key={workflow.id} className="flex items-center gap-2 rounded-xl border px-3 py-2.5" style={{ borderColor: theme.node.stroke, background: theme.node.fill }}>
                                        <div className="min-w-0 flex-1">
                                            <div className="truncate text-sm font-medium">{workflow.title}</div>
                                            <div className="truncate text-xs" style={{ color: theme.node.muted }}>
                                                {workflow.nodes.length} 个节点 · {workflow.connections.length} 条连线
                                                {workflow.variables.length ? ` · ${workflow.variables.length} 个变量` : ""}
                                                {workflow.description ? ` · ${workflow.description}` : ""}
                                            </div>
                                        </div>
                                        <Button size="small" type="primary" onClick={() => setPending(workflow)}>
                                            使用
                                        </Button>
                                        <Tooltip title="导出 JSON">
                                            <Button size="small" type="text" icon={<Download className="size-3.5" />} onClick={() => exportOne(workflow)} />
                                        </Tooltip>
                                        <Popconfirm title="删除这个工作流？" okText="删除" cancelText="取消" onConfirm={() => removeWorkflow(workflow.id)}>
                                            <Button size="small" type="text" danger icon={<Trash2 className="size-3.5" />} />
                                        </Popconfirm>
                                    </div>
                                ))}
                            </div>
                        ) : (
                            <Empty description="还没有保存过工作流" image={Empty.PRESENTED_IMAGE_SIMPLE} />
                        ),
                    },
                    {
                        key: "save",
                        label: "保存选中",
                        children: (
                            <div className="space-y-3 pt-2">
                                <div className="text-xs" style={{ color: theme.node.muted }}>
                                    已选中 {selectedCount} 个节点。保存的是<b>结构</b>——节点、连线、能力、模型、参数、提示词；不含已生成的图片视频，插回来是等待生成的空节点。
                                    <br />
                                    提示词里写 <code>{"{{主体}}"}</code> 这样的占位符，插入时可以填一次替换到所有用到的地方。
                                </div>
                                <Input value={title} placeholder="工作流名称，如「产品图 → 转视频 → 超分」" onChange={(event) => setTitle(event.target.value)} />
                                <Input.TextArea value={description} rows={2} placeholder="说明（可选）" onChange={(event) => setDescription(event.target.value)} />
                                <div className="flex items-center justify-between">
                                    <Button size="small" type="text" icon={<Upload className="size-3.5" />} onClick={importFile}>
                                        导入 JSON
                                    </Button>
                                    <Button
                                        type="primary"
                                        disabled={!selectedCount || !title.trim()}
                                        onClick={() => {
                                            onSave(title, description);
                                            setTitle("");
                                            setDescription("");
                                        }}
                                    >
                                        保存为工作流
                                    </Button>
                                </div>
                                <div className="text-xs" style={{ color: theme.node.faint }}>
                                    工作流存在本机浏览器里（刷新、重开都在），但<b>不跟账号同步</b>——换浏览器或清缓存就没了，团队共享请用「导出 JSON」。
                                </div>
                            </div>
                        ),
                    },
                ]}
            />
        </Modal>
    );
}
