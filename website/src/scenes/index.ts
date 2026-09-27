import type { Scene } from "../scene";
import { title } from "./01-title";
import { divide } from "./02-divide";
import { missing } from "./03-missing";
import { architecture } from "./04-architecture";
import { ring } from "./05-ring";
import { lanes } from "./06-lanes";
import { claims } from "./07-claims";
import { subgraph } from "./08-subgraph";
import { separation } from "./09-separation";
import { envelope } from "./10-envelope";
import { agentLoop } from "./11-agent-loop";
import { noCoordinator } from "./12-no-coordinator";
import { dashboard } from "./13-dashboard";
import { versus } from "./14-versus";
import { closing } from "./15-closing";

/** Scenes in narrative order, the order they appear on the site and in present mode. */
export const scenes: Scene[] = [
  title,
  divide,
  missing,
  architecture,
  ring,
  lanes,
  claims,
  subgraph,
  separation,
  envelope,
  agentLoop,
  noCoordinator,
  dashboard,
  versus,
  closing,
];
