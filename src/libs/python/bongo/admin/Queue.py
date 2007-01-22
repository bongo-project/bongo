import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowQueue(req):
    tmpl = Template.Template('tmpl/queue.tmpl')
    tmpl.SendPage(req)
    return 0
