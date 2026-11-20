import os, sys
# H3PE 指向实验包解包后的 h3pe 目录（默认仓库下的 .h3exp/h3pe）
H3PE = os.environ.get("H3PE") or os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", ".h3exp", "h3pe")
sys.path.insert(0, H3PE)
A = os.path.join(H3PE, "assets") + os.sep
RAMEN = "https://cdn.hailuoai.com/prod/hailuo_demo/testsets/H3_AA_I2VA/gallery/sr_v17_variants_seed42_43_20260724/inputs/4a3a90bf9100_KDmcbkhzYo5sjjxr9FqcVmWVnzb.png"
REFV = "https://cdn.hailuoai.com/prod/hailuo_demo/testsets/h3_promo_eval_ref2va/gallery/sr_v2p26_trio_seed42_20260724/inputs/297573323635_00_%E8%A7%86%E9%A2%911_YnyRbxEwio_video_20260525_163755_1927e9d3.mp4"
REFA = "https://cdn.hailuoai.com/prod/hailuo_demo/testsets/h3_promo_eval_ref2va/gallery/sr_v2p26_trio_seed42_20260724/inputs/f463d523c5ce_01_%E9%9F%B3%E9%A2%911_RSLcbpzJPo_6%E6%9C%885%E6%97%A5(1).mp3"
# media: list of (kind, src, official_role); src = local path or URL
CASES = [
 dict(id="t2v_space", task="t2v", dur=5, ratio="16:9", media=[],
      prompt="史诗级太空歌剧院线预告：女舰长独自站在巨大观景窗前，最后一支舰队正在集结并跃迁离去，强光爆闪、舰桥震动，她被留在原地。"),
 dict(id="t2v_dialog", task="t2v", dur=10, ratio="16:9", media=[],
      prompt="两个工程师在机房里争执，红色警报灯一直闪。女工程师说：“备份根本没跑，梅。”男工程师回答：“那我们今晚就告诉他们。”"),
 dict(id="t2v_product", task="t2v", dur=6, ratio="9:16", media=[],
      prompt="一瓶冰镇柠檬气泡水从木桌上缓缓升起旋转，水珠四溅，最后画面中央出现标语“清爽一夏”。"),
 dict(id="i2v_ramen", task="i2v", dur=5, ratio="adaptive", media=[("image", RAMEN, "first_frame")],
      prompt="Pull focus to the people in the background and add more steam to the ramen bowl."),
 dict(id="i2v_cat", task="i2v", dur=6, ratio="adaptive", media=[("image", A+"cat_sofa.jpg", "first_frame")],
      prompt="猫伸了个懒腰，跳下沙发，从画面右侧走出去。"),
 dict(id="l2v_soda", task="l2va", dur=5, ratio="adaptive", media=[("image", A+"soda_shop.jpg", "last_frame")],
      prompt="镜头从街角慢慢推进到汽水店门口，最后停在店门正面。"),
 dict(id="r2v_shiba", task="r2va", dur=8, ratio="16:9",
      media=[("image", A+"shiba.jpg", "reference_image"), ("image", A+"jacket.jpg", "reference_image")],
      prompt="图1的柴犬穿着图2的外套在雪地里奔跑，最后回头看镜头。"),
 dict(id="r2v_official", task="r2va", dur=5, ratio="adaptive",
      media=[("video", REFV, "reference_video"), ("audio", REFA, "reference_audio")],
      prompt="角色说话：Follow the wind, live free.Leave worries behind, enjoy the moment，音色参考音频1"),
]
def src_url(src):
    if src.startswith("http"): return src
    import lib
    return lib.prep_image(src)[0]
