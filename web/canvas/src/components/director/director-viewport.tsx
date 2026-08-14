// 3D 视口:承载 three 场景、轨道相机与取景辅助层。
//
// 相机状态是双向的:拖动轨道相机要写回工程(否则关掉再开机位就丢了),而从
// 右侧面板改数值又要推给相机。用 syncingRef 打断这个环——程序化设置相机时
// 不触发写回,否则会和用户的拖动打架。

import { useEffect, useRef } from "react";
import * as THREE from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";

import { DirectorScene } from "@/lib/director/scene";
import { aspectRatio, type DirectorCamera, type DirectorProject, type Vec3 } from "@/lib/director/project";

export type ViewportHandle = {
    capture: (longEdge?: number) => string;
    frameAll: () => void;
    setOrbit: (position: Vec3, target: Vec3) => void;
};

export function DirectorViewport({
    project,
    camera,
    selectedId,
    onSelect,
    onCameraChange,
    handleRef,
}: {
    project: DirectorProject;
    camera: DirectorCamera;
    selectedId: string | null;
    onSelect: (id: string | null) => void;
    onCameraChange: (patch: Partial<DirectorCamera>) => void;
    handleRef: React.MutableRefObject<ViewportHandle | null>;
}) {
    const canvasRef = useRef<HTMLCanvasElement>(null);
    const wrapRef = useRef<HTMLDivElement>(null);
    const sceneRef = useRef<DirectorScene | null>(null);
    const controlsRef = useRef<OrbitControls | null>(null);
    const syncingRef = useRef(false);
    const projectRef = useRef(project);
    const selectedRef = useRef(selectedId);
    const onCameraChangeRef = useRef(onCameraChange);
    const onSelectRef = useRef(onSelect);

    projectRef.current = project;
    selectedRef.current = selectedId;
    onCameraChangeRef.current = onCameraChange;
    onSelectRef.current = onSelect;

    // 场景只建一次。project 的变化走 sync,不重建——重建会丢掉轨道相机惯性。
    useEffect(() => {
        const canvas = canvasRef.current;
        const wrap = wrapRef.current;
        if (!canvas || !wrap) return;

        const scene = new DirectorScene(canvas);
        sceneRef.current = scene;
        const controls = new OrbitControls(scene.camera, canvas);
        controls.enableDamping = true;
        controls.dampingFactor = 0.08;
        controls.minDistance = 0.4;
        controls.maxDistance = 60;
        // 允许略微低于地平线,但不让相机翻到地下——地下视角除了迷惑没有用处
        controls.maxPolarAngle = Math.PI * 0.52;
        controlsRef.current = controls;

        const writeBackCamera = () => {
            if (syncingRef.current) return;
            onCameraChangeRef.current({
                position: scene.camera.position.toArray() as Vec3,
                target: controls.target.toArray() as Vec3,
                targetMode: "manual",
            });
        };
        controls.addEventListener("end", writeBackCamera);

        const observer = new ResizeObserver(() => {
            const { clientWidth, clientHeight } = wrap;
            if (clientWidth && clientHeight) scene.resize(clientWidth, clientHeight);
        });
        observer.observe(wrap);
        scene.resize(wrap.clientWidth || 1, wrap.clientHeight || 1);

        let frame = 0;
        const loop = () => {
            frame = requestAnimationFrame(loop);
            controls.update();
            scene.sync(projectRef.current, selectedRef.current);
            scene.render();
        };
        loop();

        handleRef.current = {
            capture: (longEdge) => scene.capture(projectRef.current.aspect, longEdge),
            frameAll: () => {
                const box = scene.contentBounds();
                const center = box.getCenter(new THREE.Vector3());
                const radius = Math.max(0.6, box.getSize(new THREE.Vector3()).length() / 2);
                // 按当前 FOV 反算「刚好装下」的距离,再留 40% 余量,避免主体贴边
                const distance = (radius / Math.sin((scene.camera.fov * Math.PI) / 360)) * 1.4;
                const direction = scene.camera.position.clone().sub(controls.target).normalize();
                if (!direction.lengthSq()) direction.set(0.6, 0.35, 1).normalize();
                syncingRef.current = true;
                controls.target.copy(center);
                scene.camera.position.copy(center.clone().add(direction.multiplyScalar(distance)));
                controls.update();
                syncingRef.current = false;
                writeBackCamera();
            },
            setOrbit: (position, target) => {
                syncingRef.current = true;
                scene.camera.position.fromArray(position);
                controls.target.fromArray(target);
                controls.update();
                syncingRef.current = false;
                onCameraChangeRef.current({ position, target, targetMode: "manual" });
            },
        };

        return () => {
            cancelAnimationFrame(frame);
            observer.disconnect();
            controls.removeEventListener("end", writeBackCamera);
            controls.dispose();
            scene.dispose();
            sceneRef.current = null;
            controlsRef.current = null;
            handleRef.current = null;
        };
    }, [handleRef]);

    // 面板改了机位数值 → 推给相机。拖动中(syncing)不推,否则会跟用户抢。
    useEffect(() => {
        const scene = sceneRef.current;
        const controls = controlsRef.current;
        if (!scene || !controls) return;
        if (Math.abs(scene.camera.fov - camera.fov) > 0.01) {
            scene.camera.fov = camera.fov;
            scene.camera.updateProjectionMatrix();
        }
        const target = camera.targetMode === "auto" ? scene.contentBounds().getCenter(new THREE.Vector3()) : new THREE.Vector3().fromArray(camera.target);
        const positionChanged = scene.camera.position.distanceToSquared(new THREE.Vector3().fromArray(camera.position)) > 1e-6;
        const targetChanged = controls.target.distanceToSquared(target) > 1e-6;
        if (!positionChanged && !targetChanged) return;
        syncingRef.current = true;
        scene.camera.position.fromArray(camera.position);
        controls.target.copy(target);
        controls.update();
        syncingRef.current = false;
    }, [camera]);

    const handlePointerDown = (event: React.PointerEvent<HTMLCanvasElement>) => {
        // 只有「没拖动过」的左键才算选择:拖动是在转相机,不该顺手改选区
        const startX = event.clientX;
        const startY = event.clientY;
        const canvas = event.currentTarget;
        const onUp = (up: PointerEvent) => {
            canvas.removeEventListener("pointerup", onUp);
            if (Math.hypot(up.clientX - startX, up.clientY - startY) > 4) return;
            const scene = sceneRef.current;
            if (!scene) return;
            const rect = canvas.getBoundingClientRect();
            const ndcX = ((up.clientX - rect.left) / rect.width) * 2 - 1;
            const ndcY = -((up.clientY - rect.top) / rect.height) * 2 + 1;
            onSelectRef.current(scene.pick(ndcX, ndcY));
        };
        canvas.addEventListener("pointerup", onUp);
    };

    const ratio = aspectRatio(project.aspect);

    return (
        <div ref={wrapRef} className="relative min-h-0 flex-1 overflow-hidden">
            <canvas ref={canvasRef} className="block h-full w-full" onPointerDown={handlePointerDown} />
            {/* 取景遮罩与构图辅助线画在 DOM 层而不是场景里:截图走离屏渲染,
                天然不会把这些辅助元素带进产物 */}
            <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
                <div className="relative shadow-[0_0_0_9999px_rgba(24,22,20,0.42)]" style={{ aspectRatio: String(ratio), width: "min(100%, calc(100% - 0px))", maxWidth: "100%", maxHeight: "100%", ...(ratio < 1 ? { width: "auto", height: "100%" } : {}) }}>
                    <div className="absolute inset-0 border border-white/45" />
                    {project.showThirds ? (
                        <>
                            <div className="absolute inset-y-0 left-1/3 w-px bg-white/35" />
                            <div className="absolute inset-y-0 left-2/3 w-px bg-white/35" />
                            <div className="absolute inset-x-0 top-1/3 h-px bg-white/35" />
                            <div className="absolute inset-x-0 top-2/3 h-px bg-white/35" />
                        </>
                    ) : null}
                </div>
            </div>
        </div>
    );
}
