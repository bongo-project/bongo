import Handler, Template
from bongo import MDB

def ShowLogin(req, message=''):
    username = ''
    if req.fields.has_key('username'):
        username = req.fields.getfirst('username').value
    tmpl = Template.Template('tmpl/login.tmpl')
    tmpl.SetPageVariable('message', message)
    tmpl.SetPageVariable('username', username)
    tmpl.SendPage(req)
    return 0

def Login(req):
    redirect_uri = req.application_path

    if not req.fields.has_key('username'):
        return ShowLogin(req, 'Username is required')
    if not req.fields.has_key('password'):
        return ShowLogin(req, 'Password is required')

    username = req.fields.getfirst('username').value
    password = req.fields.getfirst('password').value
    if not username.startswith('\\'):
        # look for relative users in the default context
        username = req.default_context + '\\' + username
    try:
        mdb = MDB.MDB(username, password)
    except MDB.MDBError, e:
        try:
            username = req.fields.getfirst('username').value
            mdb = MDB.MDB(username,password)
        except MDB.MDBError, e:
           return ShowLogin(req, 'Login failed')
    req.session['username'] = username
    req.session['password'] = password
    
    if req.session.has_key('request_uri'):
        redirect_uri = req.session['request_uri']
        del req.session['request_uri']
    req.session.save()
    return Handler.SendRedirect(req, redirect_uri)

def Logout(req):
    req.session.invalidate()
    return ShowLogin(req, 'Logout successful')
