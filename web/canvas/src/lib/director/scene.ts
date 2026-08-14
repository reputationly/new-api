// 导演台的 three.js 场景封装。
//
// 这一层刻意不认识 React:它只吃一个 DirectorProject,把差异同步进 three 场景,
// 并提供拾取与截图。React 侧只负责改 project 与转发鼠标事件,渲染循环、资源
// 释放、相机状态都关在这里。
//
// 同步策略是「按 id 增量对账」而不是每次全量重建:重建会丢掉正在进行的轨道
// 相机惯性,也会让每拖动一次滑杆就重新分配一遍几何体。

import * as THREE from "three";

import { applyPose, createHumanoid, setHumanoidColor, type Humanoid } from "./humanoid";
import { aspectRatio, type DirectorObject, type DirectorProject } from "./project";

type Entry = {
    object: DirectorObject;
    root: THREE.Group;
    humanoid?: Humanoid;
    mesh?: THREE.Mesh;
    /** 重建判据:体型或图元形状变了就得换几何体 */
    signature: string;
};

const D2R = Math.PI / 180;

function primitiveGeometry(shape: string): THREE.BufferGeometry {
    switch (shape) {
        case "sphere":
            return new THREE.SphereGeometry(0.5, 32, 24);
        case "cylinder":
            return new THREE.CylinderGeometry(0.5, 0.5, 1, 32);
        case "cone":
            return new THREE.ConeGeometry(0.5, 1, 32);
        case "pyramid":
            return new THREE.ConeGeometry(0.7, 1, 4);
        case "torus":
            return new THREE.TorusGeometry(0.4, 0.16, 16, 32);
        case "plane": {
            const geometry = new THREE.PlaneGeometry(1, 1);
            geometry.rotateX(-Math.PI / 2);
            return geometry;
        }
        default:
            return new THREE.BoxGeometry(1, 1, 1);
    }
}

export class DirectorScene {
    readonly scene = new THREE.Scene();
    readonly camera = new THREE.PerspectiveCamera(40, 1, 0.05, 500);
    private readonly renderer: THREE.WebGLRenderer;
    private readonly entries = new Map<string, Entry>();
    private readonly raycaster = new THREE.Raycaster();
    private readonly ground: THREE.Mesh;
    private readonly grid: THREE.GridHelper;
    private readonly selectionBox: THREE.Box3Helper;
    private readonly selectionBounds = new THREE.Box3();
    private disposed = false;

    constructor(private readonly canvas: HTMLCanvasElement) {
        this.renderer = new THREE.WebGLRenderer({ canvas, antialias: true, preserveDrawingBuffer: true });
        this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
        this.renderer.shadowMap.enabled = true;
        this.renderer.shadowMap.type = THREE.PCFSoftShadowMap;

        // 三点布光的简化版:主光带阴影定形状,补光压死黑,顶光让轮廓从背景里浮出来
        const key = new THREE.DirectionalLight(0xffffff, 2.1);
        key.position.set(4, 7, 5);
        key.castShadow = true;
        key.shadow.mapSize.set(1024, 1024);
        key.shadow.camera.near = 0.5;
        key.shadow.camera.far = 30;
        const span = 6;
        Object.assign(key.shadow.camera, { left: -span, right: span, top: span, bottom: -span });
        key.shadow.camera.updateProjectionMatrix();
        this.scene.add(key);
        this.scene.add(new THREE.HemisphereLight(0xffffff, 0x8a8378, 1.35));
        const fill = new THREE.DirectionalLight(0xffffff, 0.5);
        fill.position.set(-5, 3, -4);
        this.scene.add(fill);

        this.ground = new THREE.Mesh(new THREE.PlaneGeometry(60, 60), new THREE.MeshStandardMaterial({ color: 0xcfcac2, roughness: 0.95, transparent: true }));
        this.ground.rotation.x = -Math.PI / 2;
        this.ground.receiveShadow = true;
        this.scene.add(this.ground);

        this.grid = new THREE.GridHelper(40, 40, 0x9a948b, 0xc4bfb6);
        (this.grid.material as THREE.Material).transparent = true;
        (this.grid.material as THREE.Material).opacity = 0.55;
        this.scene.add(this.grid);

        this.selectionBox = new THREE.Box3Helper(this.selectionBounds, new THREE.Color(0x3b82f6));
        this.selectionBox.visible = false;
        this.scene.add(this.selectionBox);
    }

    /** 把工程状态同步进场景。每帧调用是安全的:没变的东西不会被重建。 */
    sync(project: DirectorProject, selectedId: string | null) {
        this.scene.background = new THREE.Color(project.scene.skyColor);
        const groundMaterial = this.ground.material as THREE.MeshStandardMaterial;
        groundMaterial.color.set(project.scene.groundColor);
        groundMaterial.opacity = project.scene.groundOpacity;
        groundMaterial.visible = project.scene.groundOpacity > 0.01;
        this.ground.position.y = project.scene.groundHeight;
        this.grid.position.y = project.scene.groundHeight + 0.001;
        this.grid.visible = project.scene.showGrid;

        const seen = new Set<string>();
        for (const object of project.objects) {
            seen.add(object.id);
            this.syncObject(object);
        }
        for (const [id, entry] of this.entries) {
            if (seen.has(id)) continue;
            this.scene.remove(entry.root);
            entry.humanoid?.dispose();
            entry.mesh?.geometry.dispose();
            this.entries.delete(id);
        }

        this.syncSelection(selectedId);
    }

    private syncObject(object: DirectorObject) {
        const signature = object.kind === "character" ? `character:${object.build}` : `primitive:${object.shape}`;
        let entry = this.entries.get(object.id);
        if (entry && entry.signature !== signature) {
            this.scene.remove(entry.root);
            entry.humanoid?.dispose();
            entry.mesh?.geometry.dispose();
            this.entries.delete(object.id);
            entry = undefined;
        }
        if (!entry) {
            const root = new THREE.Group();
            root.userData.directorId = object.id;
            entry = { object, root, signature };
            if (object.kind === "character") {
                const humanoid = createHumanoid(object.build, object.color);
                root.add(humanoid.group);
                entry.humanoid = humanoid;
            } else {
                const mesh = new THREE.Mesh(primitiveGeometry(object.shape), new THREE.MeshStandardMaterial({ color: new THREE.Color(object.color), roughness: 0.65 }));
                mesh.castShadow = true;
                mesh.receiveShadow = true;
                root.add(mesh);
                entry.mesh = mesh;
            }
            this.scene.add(root);
            this.entries.set(object.id, entry);
        }
        entry.object = object;

        const { position, rotation, scale } = object.transform;
        entry.root.position.fromArray(position);
        entry.root.rotation.set(rotation[0] * D2R, rotation[1] * D2R, rotation[2] * D2R);
        entry.root.scale.fromArray(scale);
        entry.root.visible = object.visible;

        if (entry.humanoid && object.kind === "character") {
            applyPose(entry.humanoid, object.pose);
            setHumanoidColor(entry.humanoid, object.color);
        }
        if (entry.mesh && object.kind === "primitive") {
            (entry.mesh.material as THREE.MeshStandardMaterial).color.set(object.color);
        }
    }

    private syncSelection(selectedId: string | null) {
        const entry = selectedId ? this.entries.get(selectedId) : undefined;
        if (!entry || !entry.object.visible) {
            this.selectionBox.visible = false;
            return;
        }
        entry.root.updateMatrixWorld(true);
        this.selectionBounds.setFromObject(entry.root);
        this.selectionBox.visible = !this.selectionBounds.isEmpty();
    }

    /** 屏幕坐标 → 命中的对象 id。锁定的对象不参与拾取。 */
    pick(ndcX: number, ndcY: number): string | null {
        this.raycaster.setFromCamera(new THREE.Vector2(ndcX, ndcY), this.camera);
        const targets: THREE.Object3D[] = [];
        for (const entry of this.entries.values()) {
            if (entry.object.visible && !entry.object.locked) targets.push(entry.root);
        }
        const hit = this.raycaster.intersectObjects(targets, true)[0];
        if (!hit) return null;
        let node: THREE.Object3D | null = hit.object;
        while (node && !node.userData.directorId) node = node.parent;
        return (node?.userData.directorId as string) || null;
    }

    /** 场景内容的包围盒,用于 auto 注视点与「取景到全部」 */
    contentBounds() {
        const box = new THREE.Box3();
        for (const entry of this.entries.values()) {
            if (!entry.object.visible) continue;
            entry.root.updateMatrixWorld(true);
            box.expandByObject(entry.root);
        }
        return box.isEmpty() ? new THREE.Box3(new THREE.Vector3(-0.5, 0, -0.5), new THREE.Vector3(0.5, 1.8, 0.5)) : box;
    }

    resize(width: number, height: number) {
        this.renderer.setSize(width, height, false);
        this.camera.aspect = width / Math.max(1, height);
        this.camera.updateProjectionMatrix();
    }

    render() {
        if (this.disposed) return;
        this.renderer.render(this.scene, this.camera);
    }

    /**
     * 按取景比例截图。
     *
     * 不能直接用视口画布——视口是容器比例,里面画着取景框遮罩。这里另开一张
     * 目标比例的离屏画布单独渲一次,截出来的图不带任何辅助线与遮罩。
     */
    capture(aspect: string, longEdge = 1280): string {
        const ratio = aspectRatio(aspect);
        const width = ratio >= 1 ? longEdge : Math.round(longEdge * ratio);
        const height = ratio >= 1 ? Math.round(longEdge / ratio) : longEdge;

        const target = new THREE.WebGLRenderTarget(width, height, { colorSpace: THREE.SRGBColorSpace });
        const prevAspect = this.camera.aspect;
        this.camera.aspect = ratio;
        this.camera.updateProjectionMatrix();
        const prevSelection = this.selectionBox.visible;
        this.selectionBox.visible = false;

        this.renderer.setRenderTarget(target);
        this.renderer.render(this.scene, this.camera);
        const buffer = new Uint8Array(width * height * 4);
        this.renderer.readRenderTargetPixels(target, 0, 0, width, height, buffer);
        this.renderer.setRenderTarget(null);

        this.selectionBox.visible = prevSelection;
        this.camera.aspect = prevAspect;
        this.camera.updateProjectionMatrix();
        target.dispose();

        const canvas = document.createElement("canvas");
        canvas.width = width;
        canvas.height = height;
        const context = canvas.getContext("2d");
        if (!context) return "";
        const image = context.createImageData(width, height);
        // WebGL 的原点在左下,画布在左上,逐行翻转
        for (let y = 0; y < height; y++) {
            const src = (height - 1 - y) * width * 4;
            image.data.set(buffer.subarray(src, src + width * 4), y * width * 4);
        }
        context.putImageData(image, 0, 0);
        return canvas.toDataURL("image/png");
    }

    dispose() {
        this.disposed = true;
        for (const entry of this.entries.values()) {
            entry.humanoid?.dispose();
            entry.mesh?.geometry.dispose();
        }
        this.entries.clear();
        this.ground.geometry.dispose();
        (this.ground.material as THREE.Material).dispose();
        this.grid.geometry.dispose();
        this.renderer.dispose();
    }
}
