import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowMsgserver(req):
    tmpl = Template.Template('tmpl/msgserver.tmpl')
    tmpl.SendPage(req)
    return 0
