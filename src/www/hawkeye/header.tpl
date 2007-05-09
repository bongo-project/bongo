<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN"
        "http://www.w3.org/TR/2000/REC-xhtml1-20000126/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
    <meta http-equiv="content-type" content="text/html; charset=iso-8859-1" />
    <title>Bongo Web Administration - %(breadcrumb)s</title>
    <meta http-equiv="Content-Type" content="text/html; charset=UTF-8">
	<link rel="icon" href="/img/bongo-favicon.ico" type="image/ico">
	<link rel="icon" href="/img/bongo-favicon.png" type="image/png">
	<style type="text/css" media="screen">
        @import url(/css/hawkeye.css);
	</style>
</head>
<body>
<div id="userinfo">Logged in as: <i>admin</i> [<a href="%(url|/)slogout">logout</a>]</div>
<div id="logo"><img src="/img/Bongo-logo.png" alt="Bongo" /></div>
<div id="sidebar">
<ul class="nav">
<li tal:attributes="class dsktab"><a href="%(url|/)s"><img src="/img/user-desktop.png" border="0" alt="" class="icon" /> Desktop</a></li>
<li tal:attributes="class usrtab"><img src="/img/system-users.png" alt="" class="icon" /> Users</li>
<li tal:attributes="class agntab"><a href="%(url|/)sagents"><img src="/img/system-installer.png" border="0" alt="" class="icon" /> Agents</a></li>
<li tal:attributes="class systab"><a href="%(url|/)ssystem"><img src="/img/network-server.png" border="0" alt="" class="icon" /> System</a></li>
<li tal:attributes="class hlptab"><img src="/img/help-browser.png" alt="" class="icon" /> Help</li>
</ul>
</div>
<div id="page">
<div id="content">
<div class="breadcrumb"><p>%(breadcrumb)s</p></div>

