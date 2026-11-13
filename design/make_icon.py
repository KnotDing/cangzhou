from PIL import Image, ImageDraw, ImageFilter
import math
S=1024
im=Image.new('RGBA',(S,S),(0,0,0,0)); bg=Image.new('RGBA',(S,S)); p=bg.load()
for y in range(S):
 for x in range(S):
  t=(x+y)/(2*S); glow=max(0,1-math.hypot(x-280,y-140)/1150)
  p[x,y]=(int(17+9*glow),int(57+32*glow-9*t),int(56+29*glow-6*t),255)
mask=Image.new('L',(S,S)); d=ImageDraw.Draw(mask); d.rounded_rectangle((18,18,1006,1006),radius=245,fill=255); im.paste(bg,(0,0),mask)
d=ImageDraw.Draw(im)
# Horizon halo creates depth without competing with the symbol.
d.ellipse((189,153,835,799),outline=(206,229,211,33),width=4)
# Two sails form a folded shelter above the hull.
d.polygon([(480,224),(480,602),(273,602)],fill=(242,238,218,255))
d.polygon([(520,270),(727,602),(520,602)],fill=(224,178,100,255))
d.line([(500,216),(500,638)],fill=(247,244,226,255),width=19)
# Hull: a smooth double-curve silhouette.
def bezier(p0,p1,p2,p3,n=60):
 return [tuple(round((1-t)**3*p0[i]+3*(1-t)**2*t*p1[i]+3*(1-t)*t*t*p2[i]+t**3*p3[i]) for i in (0,1)) for t in [j/n for j in range(n+1)]]
hull=bezier((188,641),(330,676),(695,676),(844,641))+bezier((844,641),(804,836),(267,849),(188,641))
d.polygon(hull,fill=(242,238,218,255))
# A small brass seal in the hull suggests secure storage.
d.ellipse((472,699,552,779),fill=(28,77,69,255))
d.rounded_rectangle((493,727,531,760),radius=7,fill=(227,185,111,255))
d.arc((496,711,528,747),180,360,fill=(227,185,111,255),width=8)
# Low waves keep the vessel grounded.
d.line(bezier((203,854),(319,817),(377,892),(509,854)),fill=(171,212,196,180),width=9)
d.line(bezier((546,854),(664,817),(724,891),(837,854)),fill=(171,212,196,180),width=9)
for size,path in [(256,'work/cangzhou-icon/icon_256.png'),(64,'work/cangzhou-icon/icon_64.png'),(512,'work/cangzhou-icon/preview.png')]:
 im.resize((size,size),Image.Resampling.LANCZOS).save(path,optimize=True)
