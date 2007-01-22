import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowCalcmd(req):
    tmpl = Template.Template('tmpl/calcmd.tmpl')
    tmpl.SendPage(req)
    return 0
