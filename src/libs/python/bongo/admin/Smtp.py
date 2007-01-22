import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowSmtp(req):
    tmpl = Template.Template('tmpl/smtp.tmpl')
    tmpl.SendPage(req)
    return 0
