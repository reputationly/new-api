// 程序化人体:用图元搭出带骨骼层级的素体,不依赖任何外部模型文件。
//
// 为什么不用 GLB:导演台只需要「体块与朝向对、能摆姿势」的参考人形,不需要皮肤
// 与面部。程序化生成省掉几百 KB 资源与模型授权,还能按体型参数实时改比例——
// 换体型只是换一组数字,不是换一个文件。
//
// 坐标约定(整套 rig 的符号都建立在这上面):
//   右手系,Y 轴向上,角色面朝 +Z,角色自身的左手边是 +X。
//   绕 X 正转:向上的骨骼向前(+Z)倒,朝前的面朝下 → 所以「点头」是 +rotX。
//   绕 Y 正转:面朝转向角色自身左侧。
//   绕 Z 正转:下垂的肢体摆向角色自身左侧(+X)。
//
// 各控制器的符号取自姿势预设的实测标定(见 poses.ts),不能随意翻转,否则
// 20 个预设会全部摆成怪姿势。注意肘与膝的弯曲方向天然相反——肘向前屈、
// 膝向后屈,这是解剖学正确而不是笔误。

import * as THREE from "three";

import type { RigPose } from "./rig";
import type { CharacterBuild } from "./project";

const D2R = Math.PI / 180;

/** 各关节在骨架里的名字。applyPose 按这些名字找 Group 并写旋转。 */
export type JointName = "root" | "pelvis" | "torso" | "chest" | "neck" | "head" | "leftShoulder" | "leftElbow" | "leftHand" | "rightShoulder" | "rightElbow" | "rightHand" | "leftHip" | "leftKnee" | "leftFoot" | "rightHip" | "rightKnee" | "rightFoot";

export type Humanoid = {
    group: THREE.Group;
    joints: Record<JointName, THREE.Group>;
    meshes: THREE.Mesh[];
    dispose: () => void;
};

/** 体型参数。基准是成年男性,其余按这些系数缩放同一套骨架。 */
type BuildSpec = {
    /** 整体身高系数 */
    height: number;
    /** 躯干与四肢的粗细 */
    girth: number;
    /** 肩宽系数 */
    shoulder: number;
    /** 髋宽系数 */
    hip: number;
    /** 头身比修正:儿童头相对更大 */
    head: number;
    /** 腿长占比修正 */
    leg: number;
};

const BUILD_SPECS: Record<CharacterBuild, BuildSpec> = {
    male: { height: 1, girth: 1, shoulder: 1, hip: 1, head: 1, leg: 1 },
    female: { height: 0.94, girth: 0.88, shoulder: 0.88, hip: 1.1, head: 0.96, leg: 1.02 },
    slim: { height: 0.99, girth: 0.78, shoulder: 0.93, hip: 0.9, head: 0.98, leg: 1.03 },
    heavy: { height: 0.98, girth: 1.42, shoulder: 1.08, hip: 1.25, head: 1.02, leg: 0.95 },
    muscular: { height: 1.02, girth: 1.26, shoulder: 1.2, hip: 1.02, head: 0.96, leg: 1 },
    teen: { height: 0.88, girth: 0.82, shoulder: 0.88, hip: 0.92, head: 1.06, leg: 1 },
    // 儿童不是等比缩小的成人:头身比明显更大、腿更短,等比缩放会看着像远处的成人
    child: { height: 0.66, girth: 0.86, shoulder: 0.86, hip: 0.94, head: 1.35, leg: 0.88 },
    mannequin: { height: 1, girth: 1.08, shoulder: 1.02, hip: 1, head: 0.9, leg: 1 },
};

/** 基准骨架尺寸(米),成年男性约 1.75m */
const BASE = {
    hipHeight: 0.95,
    pelvis: 0.12,
    torso: 0.2,
    chest: 0.2,
    neck: 0.09,
    headRadius: 0.108,
    upperArm: 0.29,
    foreArm: 0.25,
    hand: 0.09,
    thigh: 0.44,
    shin: 0.42,
    foot: 0.17,
    shoulderHalf: 0.19,
    hipHalf: 0.09,
};

function capsule(radius: number, length: number, color: THREE.ColorRepresentation, material: THREE.Material) {
    // CapsuleGeometry 的 length 是圆柱段长度,两端半球额外各加 radius。
    // 想让整根骨骼首尾正好落在关节上,圆柱段要减掉两个半径。
    const cylinder = Math.max(0.01, length - radius * 2);
    const mesh = new THREE.Mesh(new THREE.CapsuleGeometry(radius, cylinder, 6, 12), material);
    mesh.castShadow = true;
    mesh.receiveShadow = true;
    return mesh;
}

/**
 * 造一根「从关节向下延伸 length」的骨骼:
 * 返回的 Group 原点在关节处,网格挂在它下方,子关节接在末端。
 */
function bone(name: JointName, length: number, radius: number, material: THREE.Material, meshes: THREE.Mesh[]) {
    const group = new THREE.Group();
    group.name = name;
    const mesh = capsule(radius, length, 0, material);
    mesh.position.y = -length / 2;
    group.add(mesh);
    meshes.push(mesh);
    return group;
}

export function createHumanoid(build: CharacterBuild, color: string): Humanoid {
    const spec = BUILD_SPECS[build] || BUILD_SPECS.male;
    const s = spec.height;
    const g = spec.girth;
    const meshes: THREE.Mesh[] = [];
    const material = new THREE.MeshStandardMaterial({ color: new THREE.Color(color), roughness: 0.72, metalness: 0.03 });

    const dim = {
        hipHeight: BASE.hipHeight * s * spec.leg,
        pelvis: BASE.pelvis * s,
        torso: BASE.torso * s,
        chest: BASE.chest * s,
        neck: BASE.neck * s,
        headRadius: BASE.headRadius * s * spec.head,
        upperArm: BASE.upperArm * s,
        foreArm: BASE.foreArm * s,
        hand: BASE.hand * s,
        thigh: BASE.thigh * s * spec.leg,
        shin: BASE.shin * s * spec.leg,
        foot: BASE.foot * s,
        shoulderHalf: BASE.shoulderHalf * s * spec.shoulder,
        hipHalf: BASE.hipHalf * s * spec.hip,
    };

    const joints = {} as Record<JointName, THREE.Group>;
    const mk = (name: JointName) => {
        const group = new THREE.Group();
        group.name = name;
        joints[name] = group;
        return group;
    };

    // ── 中轴 ──
    const root = mk("root");
    const pelvis = mk("pelvis");
    pelvis.position.y = dim.hipHeight;
    root.add(pelvis);

    const pelvisMesh = capsule(0.115 * g * s, dim.pelvis, 0, material);
    pelvisMesh.position.y = dim.pelvis / 2;
    pelvis.add(pelvisMesh);
    meshes.push(pelvisMesh);

    // torso / chest 向上长,所以网格偏移取正
    const torso = mk("torso");
    torso.position.y = dim.pelvis;
    pelvis.add(torso);
    const torsoMesh = capsule(0.125 * g * s, dim.torso, 0, material);
    torsoMesh.position.y = dim.torso / 2;
    torso.add(torsoMesh);
    meshes.push(torsoMesh);

    const chest = mk("chest");
    chest.position.y = dim.torso;
    torso.add(chest);
    const chestMesh = capsule(0.145 * g * s, dim.chest, 0, material);
    chestMesh.position.y = dim.chest / 2;
    chest.add(chestMesh);
    meshes.push(chestMesh);

    const neck = mk("neck");
    neck.position.y = dim.chest;
    chest.add(neck);
    const neckMesh = capsule(0.045 * g * s, dim.neck, 0, material);
    neckMesh.position.y = dim.neck / 2;
    neck.add(neckMesh);
    meshes.push(neckMesh);

    const head = mk("head");
    head.position.y = dim.neck;
    neck.add(head);
    const headMesh = new THREE.Mesh(new THREE.SphereGeometry(dim.headRadius, 20, 16), material);
    headMesh.position.y = dim.headRadius;
    headMesh.scale.z = 1.08;
    headMesh.castShadow = true;
    head.add(headMesh);
    meshes.push(headMesh);
    // 鼻尖:没有五官时,这是唯一能一眼看出角色朝向的东西,截图当参考图时至关重要
    const nose = new THREE.Mesh(new THREE.ConeGeometry(dim.headRadius * 0.22, dim.headRadius * 0.42, 8), material);
    nose.position.set(0, dim.headRadius * 0.95, dim.headRadius * 1.02);
    nose.rotation.x = Math.PI / 2;
    head.add(nose);
    meshes.push(nose);

    // ── 四肢 ──
    const limb = (side: "left" | "right") => {
        const sign = side === "left" ? 1 : -1;

        const shoulder = mk(`${side}Shoulder` as JointName);
        shoulder.position.set(sign * dim.shoulderHalf, dim.chest * 0.86, 0);
        chest.add(shoulder);
        const upper = bone(`${side}Shoulder` as JointName, dim.upperArm, 0.052 * g * s, material, meshes);
        shoulder.add(...upper.children);

        const elbow = mk(`${side}Elbow` as JointName);
        elbow.position.y = -dim.upperArm;
        shoulder.add(elbow);
        const fore = bone(`${side}Elbow` as JointName, dim.foreArm, 0.044 * g * s, material, meshes);
        elbow.add(...fore.children);

        const hand = mk(`${side}Hand` as JointName);
        hand.position.y = -dim.foreArm;
        elbow.add(hand);
        const handMesh = capsule(0.036 * g * s, dim.hand, 0, material);
        handMesh.position.y = -dim.hand / 2;
        handMesh.scale.x = 1.25;
        handMesh.scale.z = 0.6;
        hand.add(handMesh);
        meshes.push(handMesh);

        const hip = mk(`${side}Hip` as JointName);
        hip.position.set(sign * dim.hipHalf, 0, 0);
        pelvis.add(hip);
        const thigh = bone(`${side}Hip` as JointName, dim.thigh, 0.072 * g * s, material, meshes);
        hip.add(...thigh.children);

        const knee = mk(`${side}Knee` as JointName);
        knee.position.y = -dim.thigh;
        hip.add(knee);
        const shin = bone(`${side}Knee` as JointName, dim.shin, 0.056 * g * s, material, meshes);
        knee.add(...shin.children);

        const foot = mk(`${side}Foot` as JointName);
        foot.position.y = -dim.shin;
        knee.add(foot);
        // 脚踝关节离地约 9cm(髋高减去大腿小腿),脚掌必须正好垫到 y=0,
        // 否则整个人悬空——地面接触是判断姿势可信度的第一眼线索。
        const ankleHeight = dim.hipHeight - dim.thigh - dim.shin;
        const soleThickness = Math.max(0.03 * s, ankleHeight * 0.6);
        const footMesh = new THREE.Mesh(new THREE.BoxGeometry(0.085 * g * s, soleThickness, dim.foot), material);
        // 脚掌向前伸,脚踝在后 1/4 处
        footMesh.position.set(0, -(ankleHeight - soleThickness / 2), dim.foot * 0.25);
        footMesh.castShadow = true;
        foot.add(footMesh);
        meshes.push(footMesh);
    };
    limb("left");
    limb("right");

    const group = new THREE.Group();
    group.add(root);

    return {
        group,
        joints,
        meshes,
        dispose: () => {
            meshes.forEach((mesh) => mesh.geometry.dispose());
            material.dispose();
        },
    };
}

/**
 * 把稀疏姿势写进骨架。
 *
 * 每次都从零位重算而不是增量旋转——增量会累积浮点误差,而且拖动滑杆时
 * 「设为 30°」和「再转 30°」是完全不同的语义。
 */
export function applyPose(humanoid: Humanoid, pose: RigPose) {
    const v = (key: keyof RigPose) => (pose[key] || 0) * D2R;
    const j = humanoid.joints;

    // 整体:offsetY 让蹲、跪这些动作能把重心压下去(骨架本身不做 IK)
    j.root.position.y = pose["body.offsetY"] || 0;
    j.root.rotation.set(-v("body.pitch"), v("body.yaw"), v("body.roll"));
    j.torso.rotation.set(-v("torso.pitch"), v("torso.yaw"), v("torso.roll"));
    // 头的俯仰用 +:预设里「点头 / 看手机」为正,表示低头
    j.head.rotation.set(v("head.pitch"), v("head.yaw"), v("head.roll"));

    for (const side of ["left", "right"] as const) {
        const shoulder = j[`${side}Shoulder` as JointName];
        const elbow = j[`${side}Elbow` as JointName];
        const hand = j[`${side}Hand` as JointName];
        const hip = j[`${side}Hip` as JointName];
        const knee = j[`${side}Knee` as JointName];
        const foot = j[`${side}Foot` as JointName];

        // 下垂的肢体:pitch 正 = 向前抬起 → 绕 X 负转;spread 正 = 摆向 -X → 绕 Z 负转
        shoulder.rotation.set(-v(`${side}Shoulder.pitch` as keyof RigPose), v(`${side}Shoulder.twist` as keyof RigPose), -v(`${side}Shoulder.spread` as keyof RigPose));
        // 肘向前屈
        elbow.rotation.set(-v(`${side}Elbow.bend` as keyof RigPose), 0, 0);
        hand.rotation.set(-v(`${side}Hand.pitch` as keyof RigPose), v(`${side}Hand.twist` as keyof RigPose), -v(`${side}Hand.roll` as keyof RigPose));

        hip.rotation.set(-v(`${side}Hip.pitch` as keyof RigPose), v(`${side}Hip.twist` as keyof RigPose), -v(`${side}Hip.spread` as keyof RigPose));
        // 膝向后屈 —— 与肘相反,解剖学如此
        knee.rotation.set(v(`${side}Knee.bend` as keyof RigPose), 0, 0);
        foot.rotation.set(v(`${side}Foot.pitch` as keyof RigPose), 0, -v(`${side}Foot.roll` as keyof RigPose));
    }
}

/** 换颜色不必重建骨架 */
export function setHumanoidColor(humanoid: Humanoid, color: string) {
    const next = new THREE.Color(color);
    for (const mesh of humanoid.meshes) {
        const material = mesh.material as THREE.MeshStandardMaterial;
        material.color.copy(next);
    }
}
