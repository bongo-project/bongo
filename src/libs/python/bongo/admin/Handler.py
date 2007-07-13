import inspect
from libbongo.libs import msgapi
from bongo import MDB
import bongo.commonweb.BongoSession as Session
import bongoutil as util
import Alias, Mdb, Security, Template, User, JsonUtil
import Imap, Pop3, Calcmd, Webservices, Itip, Webadmin, Proxy, Store, Smtp, Queue, Addressbook, Collector, Antispam, Msgserver

class Config:
    def __init__(self):
        # Module mappings
        self.modules = {}
        self.modules['main'] = ShowMain
        self.modules['js/global'] = SendGlobalJs
        self.modules['logout'] = Security.Logout
        self.modules['createuser'] = User.CreateUser
        self.modules['getuser'] = User.GetUser
        self.modules['searchusers'] = User.SearchUsers
        self.modules['getuserfeature'] = User.GetUserFeature
        self.modules['setuserfeature'] = User.SetUserFeature
        self.modules['createalias'] = Alias.CreateAlias
        self.modules['getobjattr'] = Mdb.GetObjectAttribute
        self.modules['setobjattr'] = Mdb.SetObjectAttribute
        self.modules['getattrprop'] = Mdb.GetAttributeProperty
	self.modules['setattrprop'] = Mdb.SetAttributeProperty
        self.modules['deleteobj'] = Mdb.DeleteObject
        self.modules['searchtree'] = Mdb.SearchTree
        self.modules['loadobjchildren'] = Mdb.LoadObjectChildren
        self.modules['loadobjattrs'] = Mdb.LoadObjectAttributes
        self.modules['imap'] = Imap.ShowImap
        self.modules['pop3'] = Pop3.ShowPop3
        self.modules['calcmd'] = Calcmd.ShowCalcmd
        self.modules['webservices'] = Webservices.ShowWebservices
        self.modules['itip'] = Itip.ShowItip
        self.modules['webadmin'] = Webadmin.ShowWebadmin
        self.modules['proxy'] = Proxy.ShowProxy
        self.modules['store'] = Store.ShowStore
        self.modules['smtp'] = Smtp.ShowSmtp
        self.modules['queue'] = Queue.ShowQueue
        self.modules['addressbook'] = Addressbook.ShowAddressbook
        self.modules['collector'] = Collector.ShowCollector
        self.modules['antispam'] = Antispam.ShowAntispam
        self.modules['msgserver'] = Msgserver.ShowMsgserver


        # Public modules
        self.publicmods = []
        self.publicmods.append('js/global')

        # Directories that should not be exposed
        self.hiddendirs = []
        self.hiddendirs.append('tmpl')

        # Modules that should not receive html error messages
        self.ajaxmods = []
        self.ajaxmods.append('createuser')
        self.ajaxmods.append('getuser')
        self.ajaxmods.append('searchusers')
        self.ajaxmods.append('getuserfeature')
        self.ajaxmods.append('setuserfeature')
        self.ajaxmods.append('createalias')
        self.ajaxmods.append('getobjattr')
        self.ajaxmods.append('setobjattr')
        self.ajaxmods.append('getattrprop')
	self.ajaxmods.append('setattrprop')
        self.ajaxmods.append('deleteobj')
        self.ajaxmods.append('searchtree')
        self.ajaxmods.append('loadtreeobjs')
        self.ajaxmods.append('loadtreeattrs')
        self.ajaxmods.append('imap')
	self.ajaxmods.append('pop3')
        self.ajaxmods.append('calcmd')
        self.ajaxmods.append('webservices')
        self.ajaxmods.append('itip')
        self.ajaxmods.append('webadmin')
        self.ajaxmods.append('proxy')
        self.ajaxmods.append('store')
        self.ajaxmods.append('smtp')
        self.ajaxmods.append('queue')
        self.ajaxmods.append('addressbook')
        self.ajaxmods.append('collector')
        self.ajaxmods.append('antispam')
        self.ajaxmods.append('msgserver')

def handler(req):
    req.config = Config()
    req.application_path = req.get_options()['ApplicationPath']
    req.filesystem_path = req.get_options()['FilesystemPath']
    req.resource_name = req.uri.split(req.application_path)[-1].strip('/')
    req.fields = util.FieldStorage(req)
    # FIXME: context should come from messaging servers
    req.default_context = msgapi.GetConfigProperty(msgapi.DEFAULT_CONTEXT)

    # Check for regular resource request
    if req.resource_name.find('.') >= 0:
        resource_dir = '/'.join(req.resource_name.split('/')[0:-1])
        if resource_dir not in req.config.hiddendirs:
            try:
                return SendFile(req)
            except Exception, e:
                return SendError(req, 'Unknown resource: ' + req.uri)
        else:
            return SendError(req, 'Unknown resource: ' + req.uri)

    # Load session and validate user
    req.session = Session.Session(req, lock=0)
    req.session.load()
    if req.resource_name == 'login':
        return Security.Login(req)
    if req.resource_name not in req.config.publicmods and \
           not req.session.has_key('username'):
        if req.resource_name != 'logout':
            req.session['request_uri'] = req.uri
        req.session.save()
        if req.resource_name not in req.config.ajaxmods:
            return Security.ShowLogin(req)
        else:
            return SendError(req, 'Not logged in')

    # Standard policies
    if req.resource_name.strip().strip('/') == '':
        return SendRedirect(req, req.application_path + '/main')
    if not req.config.modules.has_key(req.resource_name):
        return SendError(req, 'Unknown resource: ' + req.uri)

    # Initialize MDB and execute the module
    try:
        username = req.session['username']
        password = req.session['password']
        req.mdb = MDB.MDB(username, password)
        return req.config.modules[req.resource_name](req)
    except Exception, e:
        return SendError(req, str(e))

def ShowMain(req):
    tmpl = Template.Template('tmpl/main.tmpl')
    tmpl.SetPageVariable('username', req.session['username'])
    tmpl.SendPage(req)
    return 0

def SendFile(req):
    req.sendfile(req.filesystem_path + "/" + req.resource_name)
    return 0

# Use the X-JSON header to send response data.
# Presence of a response body indicates an error (with the text explaining
# the error).

def SendJson(req, obj):
    # Use parenthesis to avoid mozilla issues
    req.headers_out['X-JSON'] = '(' + JsonUtil.jsonstr(obj) + ')'

    return 0

def SendMessage(req, message=''):
    return SendJson(req, message)

def SendDict(req, values={}):
    #return SendError(req, JsonUtil.jsonstr(values))
    return SendJson(req, values)

def SendList(req, values=[]):
    return SendJson(req, values)

def SendError(req, message=''):
    if req.resource_name  in req.config.ajaxmods:
        req.write(message)
        return 0

    tmpl = Template.Template('tmpl/error.tmpl')
    tmpl.SetPageVariable('message', message)
    tmpl.SendPage(req)
    return 0

def SendRedirect(req, url):
    util.redirect(req, url)
    return 0

def SendGlobalJs(req):
    for key, val in inspect.getmembers(MDB.MDB):
        if not key.startswith('C_') and not key.startswith('A_'):
            continue
        req.write('var %s = \"%s\";\n' % (str(key), str(val)))
    for key, val in inspect.getmembers(msgapi):
        if not key.startswith('C_') and not key.startswith('A_'):
            continue
        req.write('var MSGSRV_%s = \"%s\";\n' % (str(key), str(val)))
    return 0
