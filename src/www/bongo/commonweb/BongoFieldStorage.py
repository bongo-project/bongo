from StringIO import StringIO

# Apache implementation follows
def get_fieldstorage_apache(req):
    return util.FieldStorage(req)

# Python implementation follows
def get_fieldstorage_python(req):
    env = {'REQUEST_METHOD': 'POST'}
    length = int(req.headers_in["content-length"])
    return cgi.FieldStorage(fp=StringIO(req.read(length)),
                            headers=req.headers_in, environ=env)

try:
    import _apache
    from mod_python import util
    get_fieldstorage = get_fieldstorage_apache
except ImportError:
    import cgi
    get_fieldstorage = get_fieldstorage_python
