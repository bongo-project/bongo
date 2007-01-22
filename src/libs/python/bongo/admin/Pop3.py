import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowPop3(req):
    tmpl = Template.Template('tmpl/pop3.tmpl')
    tmpl.SendPage(req)
    return 0
