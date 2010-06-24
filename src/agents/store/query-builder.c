
/** 
 * \file QueryBuilder is used to translate the queries and other options various Store commands
 * take into an SQL expression which can be run against the store database. In general, these 
 * are one specific class of query: querying storeobjects, filtering them, retrieving their
 * various properties, etc.
 */

#include <memmgr.h>
#include <bongoutil.h>
#include <logger.h>
#include "query-builder.h"
#include "query-parser.h"
#include "properties.h"

static int QueryExpressionToSQL(QueryBuilder *builder, struct expression *exp, BongoStringBuilder *sb);
static int QueryBuilderAddProperty(QueryBuilder *builder, const char *property, BOOL output);

/**
 * Start a new Query Builder. This is used to turn queries and other data into
 * complete SQL queries that can then be run.
 * /param	builder	QueryBuilder instance we're setting up
 * /return	0 on success, error codes otherwise
 */
int
QueryBuilderStart(QueryBuilder *builder)
{
	memset(builder, 0, sizeof(QueryBuilder));
	builder->order_prop = builder->int_query = builder->ext_query =  NULL;
	builder->order_direction = ORDER_NONE;
	builder->output_mode = MODE_COLLECTIONS;
	
	builder->properties = g_array_sized_new(FALSE, FALSE, sizeof(StorePropInfo *), 20);
	builder->links = g_array_sized_new(FALSE, FALSE, sizeof(ExtraLink *), 10);
	builder->parameters = g_array_sized_new(FALSE, FALSE, sizeof(QueryBuilder_Param *), 10);
	
	return 0;
}

/**
 * Finish the Query Builder we were using.
 * \param	builder	QueryBuilder instance we're tearing down
 */
void
QueryBuilderFinish(QueryBuilder *builder)
{
	unsigned int x;
	StorePropInfo *newprop;
	ExtraLink *link;
	QueryBuilder_Param *param;

	QueryParserFinish(&builder->internal_parser);
	QueryParserFinish(&builder->external_parser);
	
	/* free all the properties */
	for(x=0;x<builder->properties->len;x++) {
		newprop = g_array_index(builder->properties, StorePropInfo *, x);
		MemFree(newprop);
	}
	g_array_free(builder->properties, TRUE);

	/* free all the links */
	for(x=0;x<builder->links->len;x++) {
		link = g_array_index(builder->links, ExtraLink *, x);
		MemFree(link);
	}
	g_array_free(builder->links, TRUE);

	/* free all the parameters */
	for(x=0;x<builder->parameters->len;x++) {
		param = g_array_index(builder->parameters, QueryBuilder_Param *, x);
		MemFree(param);
	}
	g_array_free(builder->parameters, TRUE);
}

/**
 * Set the query string into the query we're building.
 * \param	builder	the QueryBuilder instance we're dealing with
 * \param	query	the query we want it to process
 * \param	internal	whether or not the query has come from within Bongo code
 * \return	0 on success, error codes otherwise.
 */
static int
QueryBuilderSetQuery(QueryBuilder *builder, char const *query, BOOL internal)
{
	if (internal) {
		builder->int_query = query;
	} else {
		builder->ext_query = query;
	}
	
	return 0;
}

int
QueryBuilderSetQuerySafe(QueryBuilder *builder, const char *query)
{
	return QueryBuilderSetQuery(builder, query, TRUE);
}

int
QueryBuilderSetQueryUnsafe(QueryBuilder *builder, const char *query)
{
	return QueryBuilderSetQuery(builder, query, FALSE);
}

/**
 * Set the order of results and which property to order on
 * \param	builder	the querybuilder we're using
 * \param	prop	the property to order by
 * \param	asc	whether or not the results should be in ascending order
 * \return	0 for success, error codes otherwise
 */
int
QueryBuilderSetResultOrder(QueryBuilder *builder, char const *prop, BOOL asc)
{
	builder->order_prop = prop;
	
	if (asc) 
		builder->order_direction = ORDER_ASC;
	else
		builder->order_direction = ORDER_DESC;
	
	// need to add the prop to the query in case it's not something
	// we'd usually pull out.
	return QueryBuilderAddProperty(builder, prop, FALSE);
}

/**
 * Tell later users how much information we want about each document
 * \param	builder	the querybuilder we're using
 * \return	0	always successful
 */
int
QueryBuilderSetOutputMode(QueryBuilder *builder, QueryBuilder_OutputMode mode)
{
	builder->output_mode = mode;
	return 0;
}

/**
 * Set the range on the query in terms of where we start and finish in 
 * the results. These refer to the individual "rows" of results, where
 * results are only returned between these two positions.
 * \param	builder	the querybuilder we're interested in
 * \param	start	Index into the start of the results.
 * \param	end	Index into the end of the result.
 * \return	0 (always succeeds)
 */
int
QueryBuilderSetResultRange(QueryBuilder *builder, int start, int end)
{
	builder->limit_start = start;
	builder->limit_end = end;
	return 0;
}

/**
 * Add properties to be bound to the resulting SQL query. This is just
 * a convenience really - we're not going to use this data within the
 * builder, but it will be needed with the resulting SQL.
 * Slightly ugly API, but other methods end up doing worse void casts.
 */
int
QueryBuilderAddParam(QueryBuilder *builder, int position,
	QueryBuilder_ParamType type, int d1, uint64_t d2, char *d3)
{
	QueryBuilder_Param *p;
	
	p = MemNew0(QueryBuilder_Param, 1);
	if (p == NULL) return -1;
	
	p->position = position;
	p->type = type;
	switch (type) {
		case TYPE_INT:
			p->data.d_int = d1;
			break;
		case TYPE_INT64:
			p->data.d_int64 = d2;
			break;
		case TYPE_TEXT:
			p->data.d_text = d3;
			break;
		default:
			return -1;
	}
	
	g_array_append_val(builder->parameters, p);
	return 0;
}

/**
 * Add extra results for a given query - usually, the value of a property for
 * each document returned in the query
 * \param	builder	the querybuilder we're interested in
 * \param	property	the extra property we want
 * \return	0 on success, errorcodes otherwise.
 */
int
QueryBuilderAddPropertyOutput(QueryBuilder *builder, const char *property)
{
	return QueryBuilderAddProperty(builder, property, TRUE);
}

static int
QueryBuilderAddProperty(QueryBuilder *builder, char const *property, BOOL output)
{
	StorePropInfo *newprop;
	unsigned int i;
	
	for (i=0; i < builder->properties->len; i++) {
		StorePropInfo *prop = g_array_index(builder->properties, StorePropInfo *, i);
		
		if (strcmp(prop->name, property) == 0) {
			// we already have this property
			prop->output |= output;
			return 0;
		}
	}
	
	newprop = MemNew0(StorePropInfo, 1);
	newprop->type = 0;
	newprop->name = (char *)property;
	newprop->index = builder->properties->len;
	
	StorePropertyFixup(newprop);
	
	newprop->output = output;
	g_array_append_val(builder->properties, newprop);
	return 0;
}

static int
QueryBuilderPropertyToColumn(QueryBuilder *builder, BongoStringBuilder *sb, StorePropInfo *prop)
{
	if (builder == NULL) return -1;
	
	if ((prop->table_name != NULL) && (prop->column != NULL)) {
		BongoStringBuilderAppendF(sb, "%s.%s", prop->table_name, prop->column);
	} else {
		BongoStringBuilderAppendF(sb, "prop_%d.value", prop->index);
	}
	return 0;
}

static int
QueryBuilderFindExpressionProps(QueryBuilder *builder, struct expression *exp)
{
	int retcode;
	
	// treat linked documents specially, because we need to join those in.
	if (exp->op[0] == 'l') {
		BOOL to = FALSE;
		ExtraLink *link;
		// asking for something to be linked. Sneakily, we'll replace the first
		// argument with the link struct we create. Slightly naughty.
		link = MemNew0(ExtraLink, 1);
		
		if (!exp->exp1_const) return -1; // can't have a non-const here..
		if (strncmp(exp->exp1, "to", 2) == 0) to = TRUE;
		
		if (to) {
			link->join_column = "doc_guid";
			link->test_column = "related_guid";
		} else {
			link->join_column = "related_guid";
			link->test_column = "doc_guid";
		}
		link->pos = builder->links->len;
		
		// add the new link to our list of links
		g_array_append_val(builder->links, link);
		exp->exp1 = link;	// FIXME: [linkhack] very hacky :(
		return 0;
	}
	
	if (exp->exp1_const) {
		if (QueryParser_IsProperty((char *)exp->exp1) == 0) {
			QueryBuilderAddProperty(builder, (char *)exp->exp1, FALSE);
		}
	} else {
		retcode = QueryBuilderFindExpressionProps(builder, exp->exp1);
		if (retcode != 0) return retcode;
	}
	if (exp->exp2_const) {
		if (QueryParser_IsProperty((char *)exp->exp2) == 0) {
			QueryBuilderAddProperty(builder, (char *)exp->exp2, FALSE);
		}
	} else {
		return QueryBuilderFindExpressionProps(builder, exp->exp2);
	}
	return 0;
}

int
QueryBuilderRun(QueryBuilder *builder)
{
	int retcode = 0;
	
	if (builder->int_query != NULL) {
		if (QueryParserStart(&builder->internal_parser, builder->int_query, 50)) goto abort;
		if (QueryParserRun(&builder->internal_parser, FALSE)) goto abort;
		if (QueryBuilderFindExpressionProps(builder, builder->internal_parser.start)) goto abort;
	}
	if (builder->ext_query != NULL) {
		if (QueryParserStart(&builder->external_parser, builder->ext_query, 50)) goto abort;
		if (QueryParserRun(&builder->external_parser, TRUE)) goto abort;
		if (QueryBuilderFindExpressionProps(builder, builder->external_parser.start)) goto abort;
	}
	
	goto finish;
	
abort:
	retcode = -1;
finish:
	return retcode;
}

/**
 * Create the SQL query based on the information we've gathered so far and the queries we've parsed.
 * All queries are of the form:
 * SELECT <default rows>, <optional props> FROM storeobject, <other needed tables> WHERE <conditions> 
 *   ORDER BY <some column> [ASC|DESC] LIMIT <some amount>;
 * \param	builder	The builder instance we're using
 * \param	output	a char * where we should place the output. Caller frees? (can we do this in Finish?)
 * \return	0 on success, error codes otherwise.
 */
int
QueryBuilderCreateSQL(QueryBuilder *builder, char **output)
{
	BongoStringBuilder b;
	unsigned int i;
	int ccode;
	
	if (BongoStringBuilderInit(&b)) {
		// unable to start... ick
		return -1;
	}
	
	// basic start for all queries
	BongoStringBuilderAppend(&b, "SELECT so.guid, so.collection_guid, so.imap_uid, so.filename, so.type, so.flags, so.size, so.time_created, so.time_modified");
	
	// extra columns for additional properties
	for (i=0; i < builder->properties->len; i++) {
		StorePropInfo *prop = g_array_index(builder->properties, StorePropInfo *, i);
		// only add those columns which we want returned.
		BongoStringBuilderAppend(&b, ", ");
		QueryBuilderPropertyToColumn(builder, &b, prop);
	}
	
	BongoStringBuilderAppend(&b, " FROM storeobject so");
	
	// add in the tables that we need
	if (builder->linkin_conversations) {
		BongoStringBuilderAppend(&b, " INNER JOIN conversation c ON so.guid=c.guid");
	}
	for (i=0; i < builder->links->len; i++) {
		ExtraLink *link = g_array_index(builder->links, ExtraLink *, i);
		BongoStringBuilderAppendF(&b, 
			" INNER JOIN links link_%d ON so.guid=link_%d.%s",
			i, i, link->join_column);
	}
	for (i=0; i < builder->properties->len; i++) {
		StorePropInfo *prop = g_array_index(builder->properties, StorePropInfo *, i);
		// only add those columns which we want returned.
		if ((prop->table_name == NULL) && (prop->column == NULL)) {
			// FIXME. Both following queries should use bound parameters really.
			if (prop->type == STORE_PROP_EXTERNAL) {
				BongoStringBuilderAppendF(&b, 
					" LEFT JOIN properties prop_%d ON so.guid=prop_%d.guid AND prop_%d.name=\"%s\"",
					i, i, i, prop->name);
			} else {
				BongoStringBuilderAppendF(&b,
					" LEFT JOIN properties prop_%d ON so.guid=prop_%d.guid AND prop_%d.intprop=%d",
					i, i, i, (int)prop->type);
			}
		}
	}
	
	if (builder->int_query || builder->ext_query || (builder->properties->len > 0))
		BongoStringBuilderAppend(&b, " WHERE ");
	
	// add in any constraints specified on the various columns
	if (builder->int_query) {
		if (QueryExpressionToSQL(builder, builder->internal_parser.start, &b)) {
			ccode = -2;
			goto abort;
		}
	}
	if (builder->int_query && builder->ext_query) {
		BongoStringBuilderAppend(&b, " AND ");
	}
	if (builder->ext_query) {
		if (QueryExpressionToSQL(builder, builder->external_parser.start, &b)) {
			ccode = -3;
			goto abort;
		}
	}
	
	// set the order of the query if requested
	if (builder->order_prop != NULL) {
		StorePropInfo prop;
		
		char *direction = (builder->order_direction == ORDER_ASC)?
			"ASC" : "DESC";
		
		BongoStringBuilderAppendF(&b, " ORDER BY ");
		prop.name = (char *)builder->order_prop;
		StorePropertyFixup(&prop);
		
		// FIXME: do we also need the table alias here?
		BongoStringBuilderAppendF(&b, " %s %s", prop.column, direction);
	}
	
	// set any limit on the results.
	if (builder->limit_start > 0) {
		BongoStringBuilderAppendF(&b, " LIMIT %d, %d", 
			builder->limit_start, builder->limit_end);
	}
	
	BongoStringBuilderAppend(&b, ";");
	
	*output = MemStrdup(b.value);
	printf("SQL: '%s'\n", b.value);
	
	BongoStringBuilderDestroy(&b);
	
	return 0;

abort:
	BongoStringBuilderDestroy(&b);
	return ccode;
}

static int
AddExpValueByProperty(QueryBuilder *b, BongoStringBuilder *sb, void *exp, int constant)
{
	StorePropInfo *prop = NULL;
	
	if (constant) {
		char *str = (char *)exp;
		
		if (QueryParser_IsProperty(str) == 0) {
			// FIXME: this is a slow way of finding our
			// property again.
			for (unsigned int i=0; i < b->properties->len; i++) {
				StorePropInfo *p = g_array_index(b->properties, StorePropInfo *, i);
				
				if (strcmp(p->name, str) == 0) prop = p;
			}
			
			if (prop == NULL) {
				Log(LOG_CRIT, "BUG: Unable to find property '%s'", str);
				return -1;
			}
			
			QueryBuilderPropertyToColumn(b, sb, prop);
		} else {
			if (prop && STORE_IS_EXTERNAL_PROPERTY_TYPE(prop->type)) {
				// string values need to be quoted.
				// TODO : might be more cases needed here.
				BongoStringBuilderAppendF(sb, "\"%s\"", str);
			} else {
				BongoStringBuilderAppend(sb, str);
			}
		}
	} else {
		int retcode = QueryExpressionToSQL(b, (struct expression *)exp, sb);
		if (retcode) return retcode;
	}
	
	return 0;
}

static int
QueryExpressionToSQL(QueryBuilder *builder, struct expression *exp, BongoStringBuilder *sb)
{
	const char basic_ops[] = "&|<>=!~^";
	int retcode = 0;
	
	for (unsigned int i=0; i < sizeof(basic_ops); i++) {
		if (exp->op[0] == basic_ops[i]) {
			BongoStringBuilderAppend(sb, "(");
			
			retcode = AddExpValueByProperty(builder, sb, exp->exp1, exp->exp1_const);
			if (retcode) return retcode;
			
			switch (exp->op[0]) {
				case '&':
					BongoStringBuilderAppend(sb, " AND ");
					break;
				case '|':
					BongoStringBuilderAppend(sb, " OR ");
					break;
				case '~':
					BongoStringBuilderAppend(sb, " & ");
					break;
				case '<':
				case '>':
				case '=':
					BongoStringBuilderAppendF(sb, " %c ", exp->op[0]);
					break;
				case '!':
					BongoStringBuilderAppend(sb, " != ");
					break;
				case '^':
					BongoStringBuilderAppend(sb, " LIKE ");
					break;
				default: 
					// return 0;
					break;
			}
			
			char *val = exp->exp2;
			if (exp->op[0] == '^') {
				// special case for LIKE: we need to stick % on the param
				int len = strlen(exp->exp2);
				val = malloc(len + 4);
				val[0] = '\''; val[1] = '%';
				char *expression = (char*)exp->exp2;
				for(int i=1; i < len; i++) val[i+1] = expression[i];
				val[len] = '%';
				val[len+1] = '\'';
				val[len+2] = '\0';
			}
			
			retcode = AddExpValueByProperty(builder, sb, val, exp->exp2_const);
			
			if (val != exp->exp2) free(val);
			
			if (retcode) return retcode;
			
			BongoStringBuilderAppend(sb, ")");
			
			return 0;
		}
	}
	
	if (exp->op[0] == 'l') {
		// FIXME: [linkhack] need to do this slightly better wrt. finding link
		ExtraLink *link;
		
		link = (ExtraLink *)exp->exp1;
		BongoStringBuilderAppendF(sb, "(link_%d.%s = %s)", link->pos, 
			link->test_column, (char *)exp->exp2);
		return 0;
	}
	
	if (exp->op[0] == '{') {
		// FIXME: left-substring must also not have any sub-expressions.
		StorePropInfo prop;
		prop.type = 0;
		prop.name = (char *)exp->exp1;
		StorePropertyFixup(&prop);
		BongoStringBuilderAppend(sb, "substr(");
		QueryBuilderPropertyToColumn(builder, sb, &prop);
		BongoStringBuilderAppendF(sb, ",1,%s)", (char *)exp->exp2);
		return 0;
	}
	
	// no matching operator - parser out of step with the builder.
	return -2;
}

#if 0
int
QueryBuilderTest(void)
{
	QueryBuilder builder;
	char *sql = NULL;
	
	// no error checking here.. bad... :)
	QueryBuilderStart(&builder);
	
	//QueryBuilderSetQuerySafe(&builder, "& & = nmap.type 4096 ! nmap.guid ?3 = { nmap.document ?2 ?1");
	//QueryBuilderSetQueryUnsafe(&builder, "& & & = nmap.type 2 l from 10 = { nmap.conversation.subject 5 \"hello\" | l to 6 l from 6");
	
	QueryBuilderAddPropertyOutput(&builder, "alex.custom");
	QueryBuilderAddPropertyOutput(&builder, "nmap.conversation.subject");
	
	if (QueryBuilderRun(&builder)) {
		printf("Couldn't parse the queries?\n");
	} else if (QueryBuilderCreateSQL(&builder, &sql)) {
		printf("Couldn't create the SQL\n");
	} else {
		printf("SQL: %s\n", sql);
	}
	
	QueryBuilderFinish(&builder);
	
	if (sql) MemFree(sql);
	
	printf("Done.\n");
	
	return 0;
}
#endif
