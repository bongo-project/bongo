#ifndef QUERY_BUILDER_H
#define QUERY_BUILDER_H

#include "query-parser.h"
#include <glib.h>

typedef enum {
	ORDER_NONE,
	ORDER_DESC,
	ORDER_ASC
} QueryBuilder_QueryOrder;

typedef enum {
	TYPE_INT,
	TYPE_INT64,
	TYPE_TEXT
} QueryBuilder_ParamType;

typedef enum {
	MODE_LIST,
	MODE_COLLECTIONS,
	MODE_PROPGET
} QueryBuilder_OutputMode;

typedef struct {
	int position;
	QueryBuilder_ParamType type;
	union {
		int d_int;
		uint64_t d_int64;
		char *d_text;
	} data;
} QueryBuilder_Param;

typedef struct {
	int pos; // FIXME: [linkhack]
	char *join_column;
	char *test_column;
} ExtraLink;

typedef struct {
	BOOL linkin_conversations;	// whether or not we want to access conv. data
	
	// properties we reference in the queries
	GPtrArray *properties;		// what their names are
	
	// links we reference in the queries
	GPtrArray *links;		// linked documents to find
	
	// internal query - something setup by the Bongo store as part of a command
	char const *int_query;
	struct parser_state internal_parser;
	GPtrArray *parameters;
	
	// external query - additional constraints defined by the client
	char const *ext_query;
	struct parser_state external_parser;
	
	// whether or not the results should be ordered, and how
	QueryBuilder_QueryOrder order_direction;
	char const *order_prop;
	
	// whether or not the results should be limited, and to what extent.
	int limit_start;
	int limit_end;

	// which information we'd want to show afterwards
	QueryBuilder_OutputMode output_mode;
} QueryBuilder;

int	QueryBuilderStart(QueryBuilder *builder);

int	QueryBuilderSetQuerySafe(QueryBuilder *builder, char const *query);
int	QueryBuilderSetQueryUnsafe(QueryBuilder *builder, char const *query);
int	QueryBuilderSetResultRange(QueryBuilder *builder, int start, int end);
int	QueryBuilderSetResultOrder(QueryBuilder *builder, char const *prop, BOOL asc);
int	QueryBuilderSetOutputMode(QueryBuilder *builder, QueryBuilder_OutputMode mode);

int	QueryBuilderAddParam(QueryBuilder *builder, int position,
		QueryBuilder_ParamType type, int d1, uint64_t d2, char *d3);
int	QueryBuilderAddPropertyOutput(QueryBuilder *builder, const char *property);

int	QueryBuilderRun(QueryBuilder *builder);
void	QueryBuilderFinish(QueryBuilder *builder);

int	QueryBuilderCreateSQL(QueryBuilder *builder, char **output);

#endif
