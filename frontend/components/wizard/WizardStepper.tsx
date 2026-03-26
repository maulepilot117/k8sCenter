interface WizardStep {
 title: string;
 description?: string;
}

interface WizardStepperProps {
 steps: WizardStep[];
 currentStep: number;
 onStepClick?: (step: number) => void;
}

export function WizardStepper(
 { steps, currentStep, onStepClick }: WizardStepperProps,
) {
 return (
 <nav class="flex items-center justify-center mb-8">
 <ol class="flex items-center gap-2">
 {steps.map((step, index) => {
 const isCompleted = index < currentStep;
 const isCurrent = index === currentStep;
 const isClickable = isCompleted && onStepClick;

 return (
 <li key={index} class="flex items-center">
 {index > 0 && (
 <div
 class={`w-8 h-0.5 mx-1 ${
 isCompleted ?"bg-brand" :"bg-elevated"
 }`}
 />
 )}
 <button
 type="button"
 onClick={() => isClickable && onStepClick(index)}
 disabled={!isClickable}
 class={`flex items-center gap-2 px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
 isCurrent
 ?"bg-brand/10 text-brand border border-brand/30"
 : isCompleted
 ?"text-brand hover:bg-brand/5 cursor-pointer"
 :"text-text-muted cursor-default"
 }`}
 >
 <span
 class={`flex items-center justify-center w-6 h-6 rounded-full text-xs font-bold ${
 isCurrent
 ?"bg-brand text-white"
 : isCompleted
 ?"bg-brand text-white"
 :"bg-elevated text-text-muted"
 }`}
 >
 {isCompleted
 ? (
 <svg
 class="w-3.5 h-3.5"
 fill="none"
 viewBox="0 0 24 24"
 stroke="currentColor"
 stroke-width="3"
 >
 <path
 stroke-linecap="round"
 stroke-linejoin="round"
 d="M5 13l4 4L19 7"
 />
 </svg>
 )
 : index + 1}
 </span>
 <span class="hidden sm:inline">{step.title}</span>
 </button>
 </li>
 );
 })}
 </ol>
 </nav>
 );
}
