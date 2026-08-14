// 3D 导演台的姿势控制词汇表。
//
// 为什么是「语义控制器」而不是直接暴露欧拉角:直接给关节 XYZ 三个角度,用户要试
// 好几次才知道哪个轴对应「抬手」。这里每个控制器都对应一个人能直接说出来的动作
// (前举/外展/扭转),值是角度,右手系正方向由 axis + sign 一次性定死在关节映射里。
//
// 存储用稀疏字典:只记非零项。默认姿势(站立)就是空字典 {},这样预设表可读、
// 序列化后体积小,新增控制器也不会让旧数据失效。

export type RigControlKey =
    | "body.pitch"
    | "body.yaw"
    | "body.roll"
    | "body.offsetY"
    | "torso.pitch"
    | "torso.yaw"
    | "torso.roll"
    | "head.pitch"
    | "head.yaw"
    | "head.roll"
    | "leftShoulder.pitch"
    | "leftShoulder.spread"
    | "leftShoulder.twist"
    | "rightShoulder.pitch"
    | "rightShoulder.spread"
    | "rightShoulder.twist"
    | "leftElbow.bend"
    | "rightElbow.bend"
    | "leftHand.pitch"
    | "leftHand.roll"
    | "leftHand.twist"
    | "rightHand.pitch"
    | "rightHand.roll"
    | "rightHand.twist"
    | "leftHip.pitch"
    | "leftHip.spread"
    | "leftHip.twist"
    | "rightHip.pitch"
    | "rightHip.spread"
    | "rightHip.twist"
    | "leftKnee.bend"
    | "rightKnee.bend"
    | "leftFoot.pitch"
    | "leftFoot.roll"
    | "rightFoot.pitch"
    | "rightFoot.roll";

/** 稀疏姿势:只存非零控制器。缺省 = 0 = 站立 */
export type RigPose = Partial<Record<RigControlKey, number>>;

export type RigControl = {
    key: RigControlKey;
    label: string;
    min: number;
    max: number;
    /** offsetY 是米,其余都是角度 */
    unit: "deg" | "m";
};

export type RigControlGroup = {
    title: string;
    controls: RigControl[];
};

const deg = (key: RigControlKey, label: string, min: number, max: number): RigControl => ({ key, label, min, max, unit: "deg" });

/** 面板上按身体部位分组的控制器。左右成对的组紧挨着放,方便对称调节。 */
export const RIG_CONTROL_GROUPS: RigControlGroup[] = [
    {
        title: "身体",
        controls: [deg("body.pitch", "前倾", -60, 60), deg("body.yaw", "转身", -180, 180), deg("body.roll", "侧倾", -45, 45), { key: "body.offsetY", label: "升降", min: -0.9, max: 0.4, unit: "m" }],
    },
    { title: "躯干", controls: [deg("torso.pitch", "前倾", -45, 45), deg("torso.yaw", "扭转", -50, 50), deg("torso.roll", "侧倾", -35, 35)] },
    { title: "头部", controls: [deg("head.pitch", "点头", -40, 40), deg("head.yaw", "转头", -75, 75), deg("head.roll", "歪头", -35, 35)] },
    { title: "左肩", controls: [deg("leftShoulder.pitch", "前举", -60, 180), deg("leftShoulder.spread", "外展", -95, 25), deg("leftShoulder.twist", "扭转", -90, 90)] },
    { title: "右肩", controls: [deg("rightShoulder.pitch", "前举", -60, 180), deg("rightShoulder.spread", "外展", -25, 95), deg("rightShoulder.twist", "扭转", -90, 90)] },
    { title: "左肘", controls: [deg("leftElbow.bend", "弯曲", 0, 145)] },
    { title: "右肘", controls: [deg("rightElbow.bend", "弯曲", 0, 145)] },
    { title: "左腕", controls: [deg("leftHand.pitch", "屈伸", -60, 60), deg("leftHand.roll", "侧摆", -30, 30), deg("leftHand.twist", "旋转", -90, 90)] },
    { title: "右腕", controls: [deg("rightHand.pitch", "屈伸", -60, 60), deg("rightHand.roll", "侧摆", -30, 30), deg("rightHand.twist", "旋转", -90, 90)] },
    { title: "左髋", controls: [deg("leftHip.pitch", "前抬", -45, 120), deg("leftHip.spread", "外展", -55, 20), deg("leftHip.twist", "扭转", -45, 45)] },
    { title: "右髋", controls: [deg("rightHip.pitch", "前抬", -45, 120), deg("rightHip.spread", "外展", -20, 55), deg("rightHip.twist", "扭转", -45, 45)] },
    { title: "左膝", controls: [deg("leftKnee.bend", "弯曲", 0, 145)] },
    { title: "右膝", controls: [deg("rightKnee.bend", "弯曲", 0, 145)] },
    { title: "左踝", controls: [deg("leftFoot.pitch", "屈伸", -40, 40), deg("leftFoot.roll", "内外翻", -25, 25)] },
    { title: "右踝", controls: [deg("rightFoot.pitch", "屈伸", -40, 40), deg("rightFoot.roll", "内外翻", -25, 25)] },
];

export const RIG_CONTROLS: Record<RigControlKey, RigControl> = Object.fromEntries(RIG_CONTROL_GROUPS.flatMap((group) => group.controls).map((control) => [control.key, control])) as Record<RigControlKey, RigControl>;

export const RIG_CONTROL_KEYS = Object.keys(RIG_CONTROLS) as RigControlKey[];

/** 夹到控制器量程内。预设与导入的工程都要过一遍,越界值会把人摆成不可能的姿势。 */
export function clampRigValue(key: RigControlKey, value: number) {
    const control = RIG_CONTROLS[key];
    if (!control || !Number.isFinite(value)) return 0;
    return Math.min(control.max, Math.max(control.min, value));
}

/** 规范化姿势:丢掉不认识的键、夹量程、抹掉 0(稀疏存储的前提) */
export function normalizeRigPose(pose: RigPose | undefined | null): RigPose {
    if (!pose || typeof pose !== "object") return {};
    const next: RigPose = {};
    for (const [key, raw] of Object.entries(pose)) {
        if (!(key in RIG_CONTROLS)) continue;
        const value = clampRigValue(key as RigControlKey, Number(raw));
        if (value !== 0) next[key as RigControlKey] = Number(value.toFixed(2));
    }
    return next;
}

/** 左右镜像。做完一侧动作想要对称的另一侧时用,比逐条重填快得多。 */
export function mirrorRigPose(pose: RigPose): RigPose {
    const next: RigPose = {};
    for (const [key, value] of Object.entries(pose) as Array<[RigControlKey, number]>) {
        const swapped = (key.startsWith("left") ? `right${key.slice(4)}` : key.startsWith("right") ? `left${key.slice(5)}` : key) as RigControlKey;
        // 侧向量:左右互换后方向也要翻。前后/弯曲量镜像后不变。
        const flips = swapped.endsWith(".spread") || swapped.endsWith(".twist") || swapped.endsWith(".roll") || swapped.endsWith(".yaw");
        next[swapped] = clampRigValue(swapped, flips ? -value : value);
    }
    return normalizeRigPose(next);
}
